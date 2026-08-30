# oapi-hinge 使用手册

> 适用版本：V0.2.0 <br/>
> 设计动机与架构总览见 [README](../README.md)。

## 目录

- [0. 核心概念速览](#0-核心概念速览)
- [1. 快速开始（servergin）](#1-快速开始servergin)
- [2. OpenAPI 文档：构建期隔离](#2-openapi-文档构建期隔离)
- [3. 编写新框架适配器](#3-编写新框架适配器)
- [4. FAQ](#4-faq)

---

## 0. 核心概念速览

### 三层结构

| 层 | 包 | 职责 | 依赖 |
|---|---|---|---|
| 契约层 | `contract` | Handler 模板、路由分组树、响应壳、错误类型、绑定公用函数、扩展点注册表 | 无第三方依赖 |
| 运行时层 | `servergin` / `serverecho` / 自定义 | 把路由树挂到具体框架并执行请求管线 | 对应框架 |
| 文档层 | `openapi` | 从同一棵路由树生成 OpenAPI 3.1（`-tags openapi` 隔离构建） | kin-openapi |

单模块 `github.com/EdSan845D/oapi-hinge`，一个版本覆盖全部包；release 构建不包含任何文档生成依赖（openapi 包整体 `//go:build openapi` 隔离）。

### 统一 Handler 模板

所有业务接口都是同一个纯函数签名：

```go
func(ctx context.Context, q Q, b B) (r R, err error)
```

- `Q`：query / path / header 参数结构体（标签声明）
- `B`：JSON 请求体（`any` 或 `contract.NoReq` 占位表示无 body）
- `R`：响应数据（自动包装响应壳；`any` / `contract.Empty` 输出 `data: null`）

### 一次请求的完整管线

```
执行中间件（全局 + 分组继承）↓ (下面的链路可视为handler包装器,中间件执行顺序与原来保持一致)
  → 上下文装饰 decorate（最先执行，全链路共享）
  → Q 绑定（query/path/header/default）→ Q.InTransform
  → B 绑定（POST/PUT/PATCH，严格 JSON）→ B.InTransform
  → 校验（required 标签 → Validate() 方法 → AddValidator 注册的校验器）
  → Handler 调用（反射，挂载期已预计算）
  → err? → 错误解析（StatusError → StatusCoder → SetErrorMapper）→ 响应壳失败输出
  → contract.Response[R] 解包（Status/Headers/Cookies）
  → R.OutTransform
  → FileStream? → 流输出
  → 响应壳成功输出 → JSON
```

---

## 1. 快速开始（servergin）

> 本节以 gin 为例。**其他框架同理**：换成对应适配器、其余业务代码一行不改（见 [1.11](#111-其他框架同理)）。

### 1.1 安装

```bash
go get github.com/EdSan845D/oapi-hinge
```

### 1.2 最小可运行项目

```go
package main

import (
	"context"
	"net/http"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/servergin"
	"github.com/gin-gonic/gin"
)

// ---- Q：GET /api/hello?name=xxx ----
type HelloReq struct {
	Name string `query:"name" binding:"required" description:"称呼"`
	// 寒暄
	Greetings string `query:"greetings" default:"how's it going?"`
}

// ---- Handler：统一模板纯函数 ----
func Hello(ctx context.Context, req HelloReq, _ any) (map[string]string, error) {
	return map[string]string{"msg": "hello " + req.Name +", "+ req.Greetings}, nil
}

func main() {
	r := gin.Default()
	s := servergin.New()

	s.Mount(r.Group("/api"), []*contract.Group{{
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[HelloReq, any, map[string]string]{
				Method:  "GET",
				Path:    "/hello",
				Summary: "打招呼",
				Handler: Hello,
			}),
		},
	}})

	_ = r.Run(":8080")
}
```

运行后：`curl 'http://localhost:8080/api/hello?name=Cara'` →

```json
{"code":0,"data":{"msg":"hello Cara, how's it going?"},"msg":"操作成功"}
```

`RouteMeta[Q, B, R]` 泛型参数就是 Handler 的三个入参/返回类型，编译期即校验一致。完整字段：

| 字段 | 说明 |
|---|---|
| `Method` | HTTP 方法（`GET`/`POST`/`PUT`/`PATCH`/`DELETE`…） |
| `Path` | OpenAPI 风格路径，如 `/users/{id}`（自动转 gin 的 `:id`）；组内相对路径，列表路由用 `""` |
| `Summary` / `Description` | 文档摘要 / 描述 |
| `Tags` | 文档标签（与组 Tags 合并去重） |
| `DefaultStatusCode` | 成功状态码声明（文档与运行时同步），缺省 200 |
| `Envelope` | 路由级响应壳覆盖（缺省用服务级） |
| `Handler` | 统一模板函数 |

### 1.3 项目化组织

真实项目建议按「handlers + 唯一路由注册表」分层（可运行示例见 [`example/`](../example/)）：

```
app/
├── handlers/        # 统一 Handler：func(ctx, Q, B) (R, error)，纯函数
├── middleware/      # 业务中间件（gin.HandlerFunc / echo.MiddlewareFunc）
└── routes/routes.go # 路由注册表：All() 返回整棵 contract.Group 树（唯一注册入口）
```

运行时与文档生成器都从 `routes.All()` 取数——**注册一次，运行和文档同时就绪**。

分组树支持嵌套继承：

```go
func All() []*contract.Group {
	return []*contract.Group{
		{ // 根组：无前缀
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[handlers.NoReq, any, map[string]string]{
					Method: "GET", Path: "/health", Summary: "健康检查", Handler: handlers.Health,
				}),
			},
		},
		{ // /users 组：中间件随树向子组继承，Tags 自动合并
			Prefix:      "/users",
			Description: "用户相关接口",
			Tags:        []string{"用户"},
			Middlewares: []any{middleware.Auth},
			Routes: []contract.Route{ /* ... */ },
			Children: []*contract.Group{ /* /users/admin 等子组 */ },
		},
	}
}
```

### 1.4 声明 Q：query / path / header 参数

```go
type ListUsersReq struct {
	Page    int       `query:"page" default:"1" description:"页码"`
	Size    int       `query:"size" default:"10"`
	ID      int       `path:"id"`                          // 路由写 /users/{id}
	Token   string    `header:"X-Token"`                   // 请求头
	Keyword string    `query:"keyword" binding:"required"` // 必填
	Created time.Time `query:"created"`                    // RFC3339，如 2026-08-28T00:00:00Z
}
```

| 标签 | 作用 | 文档联动 |
|---|---|---|
| `query:"page"` | query 参数 | ✅ 生成 query 参数 |
| `path:"id"` | 路径参数（类型/描述取自字段） | ✅ 生成 path 参数 |
| `header:"X-Token"` | 请求头 | ✅ 生成 header 参数 |
| `form:"f"` | 与 query 等价 | ✅ |
| `default:"1"` | **运行时**缺省值（未传时填充） | ✅ 同步生成 default |
| `description:"..."` | 字段描述 | ✅ |
| `example:"u123"` | 示例值（按字段类型转型，数字/bool 自动转换） | ✅ 生成 example |
| `validate:"oneof=..."` / `min` / `max` / `gte` / `lte` / `email` / `url` | **约束即文档**：校验标签同步为 schema 约束（enum / minLength / minimum / format…），`binding` 标签同等参与 | ✅ |
| `binding:"required"` / `validate:"required"` | 必填（零值即缺失） | ✅ 生成 required |

**支持的类型**：string、各宽度整数/无符号、浮点、bool、`time.Time`（RFC3339）、指针（`*int` 缺省为 nil，可区分"未传"）、切片。

**切片语义**：重复参数 `?ids=1&ids=2` 与逗号串 `?ids=1,2` 等价 → `[]int{1,2}`；`[]string` 只认重复参数（不拆逗号，保留原始值）。

**内嵌结构体**自动展平（含未导出类型内嵌，如 `type Req struct { Pager; Name string }`），标签照常生效。

**自定义类型绑定（ParamBinder）**：任意业务类型可注册为"原始字符串 → 字段值"的绑定器，适合逗号串转命名切片、ID 直查缓存实体等场景：

```go
type IDs []int64

func init() {
	contract.RegisterParamBinder(func(src []string) (IDs, error) {
		// src：重复参数原值列表（?ids=1&ids=2 → ["1","2"]；逗号拆分自行处理）
		var out IDs
		for _, s := range src {
			for _, part := range strings.Split(s, ",") {
				v, err := strconv.ParseInt(part, 10, 64)
				if err != nil {
					return nil, contract.BadRequest("invalid id: " + part) // 汇入统一错误链
				}
				out = append(out, v)
			}
		}
		return out, nil
	})
}
```

注册后该类型字段自动走绑定器（path/query/form/header 均可）；参数缺失时字段保持零值（required 交给校验器兜底）；OpenAPI 中该参数 schema 自动标注为 string。

### 1.5 声明 B：JSON 请求体

```go
type CreateUserReq struct {
	Name  string `json:"name" binding:"required"`
	Email string `json:"email" validate:"required,email"` // 配合 Playground 校验器
}

func (r *CreateUserReq) InTransform(ctx context.Context) error { // 可选：绑定后自动规范化
	r.Name = strings.TrimSpace(r.Name)
	return nil
}
```

- 仅 `POST` / `PUT` / `PATCH` 解析 body；`B = any`（或接口类型）表示无 body，跳过绑定与校验
- 固定 JSON 解码（`Content-Type` 不影响行为，gin/echo 语义一致）
- `Content-Length = 0` 时保持零值（body 可省略）

### 1.6 声明 R：响应与状态码

**普通值** → 统一壳 `{code, data, msg}`：

```go
func GetUser(...) (User, error) { return u, nil }
// → 200 {"code":0,"data":{...},"msg":"操作成功"}
```

**无数据** → `contract.Empty`（或 `any`），输出 `data: null`。

**声明成功状态码**（文档同步生效）：

```go
contract.New(contract.RouteMeta[contract.NoReq, CreateUserReq, User]{
	Method: "POST", DefaultStatusCode: 201, Handler: CreateUser,
})
```

**动态返回值**：需要按次覆盖状态码/响应头/Cookie 时返回 `contract.Response[R]`：

```go
func Login(ctx context.Context, _ contract.NoReq, req LoginReq) (contract.Response[User], error) {
	u, err := doLogin(req)
	if err != nil {
		return contract.Response[User]{}, err
	}
	return contract.Response[User]{
		Status:  http.StatusCreated,
		Headers: map[string]string{"X-Trace": "t-1"},
		Cookies: []*http.Cookie{{Name: "sid", Value: "abc", HttpOnly: true}},
		Data:    u,
	}, nil
}
```

优先级：`Response[R].Status`（单次）> `DefaultStatusCode`（路由级）> 200。

**二进制下载**：返回 `*contract.FileStream`（值类型 `contract.FileStream` 亦可）：

```go
func Download(ctx context.Context, req DownloadReq, _ any) (*contract.FileStream, error) {
	f, err := os.Open(req.Name)
	if err != nil {
		return nil, contract.NotFound("文件不存在")
	}
	st, _ := f.Stat()
	return &contract.FileStream{
		Name:        filepath.Base(req.Name), // Content-Disposition 文件名
		Size:        st.Size(),               // <=0 时按分块传输
		ContentType: "application/pdf",
		Reader:      f,
	}, nil
}
```

数据源可以是文件、`go:embed` 内存数据或任何 `io.Reader`。

### 1.7 错误处理

```go
return User{}, contract.NotFound("用户不存在")
// → HTTP 404 {"code":404,"data":null,"msg":"用户不存在"}
```

便捷构造器：`BadRequest`(400) / `Unauthorized`(401) / `Forbidden`(403) / `NotFound`(404) / `Conflict`(409) / `Internal`(500)。

| 场景 | HTTP | 业务 code | msg |
|---|---|---|---|
| 成功 | 路由声明的成功码（默认 200） | 0 | SuccessMsg（默认"操作成功"） |
| 普通业务错误 | 200（存量默认） | 7 | `err.Error()` |
| `StatusError` | 自带 status | `Code`，缺省跟随 status | `Msg`，缺省 `err.Error()` |
| 自定义 `StatusCoder` | `StatusCode()` | 7 | `err.Error()` |
| 绑定/校验失败 | `bindStatus`（默认 200） | 7（非 200 时跟随） | 错误信息 |

要点：

- **错误链穿透**：`fmt.Errorf("db: %w", contract.Forbidden("无权限"))` 包装后仍被正确识别（`errors.As`）
- **内部原因不外泄**：`contract.WithCause(statusErr, innerErr)` 把细节挂进错误链（日志可见），对外只输出 `Msg`
- **全局兜底**：`s.SetErrorMapper(func(err) (httpStatus, bizCode))` 只对不携带状态码的普通错误生效
- **RESTful 化**：`s.SetBindErrorStatus(http.StatusBadRequest)` 让参数绑定/校验失败返回 400（默认 200 + code7 是兼容存量行为）；配合 `s.SetEnvelope(response.RawEnvelope{})` 可整体切裸输出风格
- 自定义错误类型只需实现 `StatusCoder` 接口（`error + StatusCode() int`）即可携带状态码

### 1.8 参数校验

校验时机：Q/B 绑定与 `InTransform` 之后、Handler 之前。依次执行：

1. **内置必填标签**：`binding:"required"` / `validate:"required"`（零值即缺失，含内嵌结构体递归）
2. **结构体自带 `Validate() error` 方法**
3. **`AddValidator` 注册的自定义校验器**（按注册顺序）

```go
s := servergin.New()

// 内置规则够用时：什么都不用加
// 需要完整规则（email/min/oneof/自定义 tag…）时一行接入 go-playground：
s.AddValidator(validator.Playground()) // 支持 validate:"required,email,min=8"

// 自定义校验器：拿到 method + 解析后的 Q/B
s.AddValidator(func(ctx context.Context, method string, q, b any) error {
	if req, ok := b.(*CreateUserReq); ok && req.Name == "admin" {
		return contract.Forbidden("reserved name")
	}
	return nil
})
```

不调用 `Playground()` 则 go-playground 不编译进二进制。注意内置 required 是"零值检查"（空切片可通过），要 go-playground 完整语义（空切片也算缺）就用 Playground。

### 1.9 入参转换 / 出参加工

实现接口即可，适配器自动调用，Handler 里不用手动处理：

```go
// Q/B 绑定后、校验前：trim 后能通过 required
func (r *CreateUserReq) InTransform(ctx context.Context) error {
	r.Name = strings.TrimSpace(r.Name)
	r.Email = strings.ToLower(r.Email)
	return nil
}

// 序列化前：脱敏（Q 与 B 都支持 InTransform；R 支持 OutTransform）
func (u *MaskedUser) OutTransform(ctx context.Context) error {
	if at := strings.Index(u.Email, "@"); at > 1 {
		u.Email = u.Email[:1] + "***" + u.Email[at:]
	}
	return nil
}
```

建议用指针接收者（修改写回）。返回错误会短路请求（走绑定/校验失败的错误路径）。

### 1.10 中间件、上下文注入

**三层中间件**：

```go
s.Use(rateLimit, recover)                 // ① 全局：所有路由之前
{ Middlewares: []any{middleware.Auth} }   // ② 分组：写在 Group.Middlewares，随树继承
r.Use(gin.Logger())                       // ③ 也可以直接用框架原生方式
```

分组中间件类型必须匹配当前框架（gin 放 `gin.HandlerFunc` / `func(*gin.Context)`），类型不对在**挂载期直接 panic** 并指明组名——静默忽略等于隐形成中间件。

**上下文注入**（装饰器）：

```go
s.SetContextDecorator(func(c *gin.Context, ctx context.Context) context.Context {
	// 每个请求（含校验失败的）都会执行 → 保持轻量，重操作放中间件
	if u := parseToken(c.GetHeader("Authorization")); u != nil {
		ctx = contract.WithUser(ctx, u) // Handler 里 contract.CurrentUser(ctx) 读取
	}
	return contract.WithFramework(ctx, c) // 保留框架
})
```

⚠️ `SetContextDecorator` 是**整体替换**默认装饰器（默认行为是注入 `contract.WithFramework(ctx, c)`）。自定义时若还想用 `contract.Framework(ctx)` 拿框架上下文，请像上面一样手动链上。

**优先级**（模板覆盖不了时按序升级）：

1. `header` 标签绑定请求头
2. `contract.Response[R]`：单次定制状态码/响应头/Cookie
3. `contract.Framework(ctx)`：直接拿 `*gin.Context`（业务层与框架耦合，仅最后手段）

### 1.11 其他框架同理

同一棵路由树换框架只差装配代码。echo 版：

```go
import (
	serverecho "github.com/EdSan845D/oapi-hinge/serverecho"
	"github.com/labstack/echo/v4"
)

e := echo.New()
s := serverecho.New()
s.AddValidator(validator.Playground())
s.Mount(e.Group(routes.BasePath), routes.All()) // 同一份 routes.All()
```

差异只有三处：适配器包名、`Group.Middlewares` 里的中间件类型（`echo.MiddlewareFunc`）、装饰器签名的 `echo.Context`。绑定规则、错误语义、响应壳、文档生成完全一致。更多框架（chi、fiber…）接入见[第 3 节](#3-编写新框架适配器)。

---

## 2. OpenAPI 文档：构建期隔离

### 2.1 设计：为什么隔离

- 文档生成器整包带 `//go:build openapi` 标签，`go build`（release）**完全不包含** kin-openapi / yaml 等开发期依赖
- 运行时与文档生成器消费**同一棵路由树**（`routes.All()`），类型即契约，不会出现文档与实现漂移
- `build.sh -r` 会自动校验 release 依赖链（`go list -deps` 检查 openapi3），防止误引入

### 2.2 接入三步

**① 新建 `main_doc.go`**（与 `main.go` 用构建标签互斥）：

```go
//go:build openapi

package main

import (
	"flag"
	"fmt"

	"yourapp/app/middleware"
	"yourapp/app/routes"
	"github.com/EdSan845D/oapi-hinge/openapi"

	"github.com/getkin/kin-openapi/openapi3"
)

func init() {
	// 可选：中间件文档钩子（见 2.4）
	openapi.RegisterMiddlewareDoc(middleware.Auth, func(op *openapi3.Operation) {
		op.Security = &openapi3.SecurityRequirements{{"BearerAuth": {}}}
		op.Responses.Set("401", &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription("Unauthorized：token 缺失或无效")})
	})
}

func main() {
	out := flag.String("out", "openapi.yaml", "openapi 文档输出路径（.yaml/.yml → YAML，.json → JSON）")
	flag.Parse()

	if err := openapi.Generate(*out, routes.All(),
		openapi.OptionWithDocInfo(&openapi3.Info{Title: "yourapp API", Version: "1.0.0"}),
		openapi.OptionWithServer(&openapi3.Servers{{URL: routes.BasePath}}),
	); err != nil {
		panic(err)
	}
	fmt.Println("openapi spec written to", *out)
}
```

**② 生成**：

```bash
go run -tags openapi . -out openapi.yaml
# 或项目带 build.sh 时：./build.sh -s
```

**③ 提交/发布 spec**：`openapi.yaml` 可以提交进仓库（供 CI 校验漂移、给 /docs 页面用），也可以只在发布流水线生成。

### 2.3 文档选项（Option）

| Option | 作用 |
|---|---|
| `OptionWithDocInfo(info)` | 标题 / 版本 / 描述 |
| `OptionWithServer(servers)` | server 列表（如 `/api`） |
| `OptionWithSecurity(schemes)` | 安全方案定义（如 BearerAuth，配合钩子标注到 operation） |
| `OptionWithEnvelope(env)` | 注入运行时实际壳实例：成功/失败响应 schema 由壳推导（见 2.5，推荐） |
| `OptionWithEnvelopeSchema(fn)` | 手写壳 schema 钩子函数（仅 map 形态壳等无法推导的场景） |

### 2.4 中间件文档钩子

中间件效果无法从类型反射得出，用"函数名 → 钩子"的映射补文档（`contract.FuncName` 反射派生，无需手写名字符串）：

```go
openapi.RegisterMiddlewareDoc(middleware.Auth, func(op *openapi3.Operation) {
	op.Security = &openapi3.SecurityRequirements{{"BearerAuth": {}}}
	op.Responses.Set("401", &openapi3.ResponseRef{Value: openapi3.NewResponse().
		WithDescription("Unauthorized：token 缺失或无效")})
})
```

- 钩子可改 operation 的任意字段（security、参数、响应码…）
- 未注册钩子的中间件照常运行，只是不进文档
- 401 / security 这类鉴权标注**由钩子按需声明**（生成器不会给公开接口硬编码 401）
- 同名中间件重复注册会 panic（防手滑）

### 2.5 响应壳：运行时推导（消灭手工配对）

壳 schema 默认由**实际生效的壳实例**推导：生成期调用壳的 `Success/Failure` 反射出形态，文档与运行时同构——想不一致都难。

```go
// 壳实例放业务共享包，两个构建引用同一份（唯一来源）：
// app/routes 或 config 包
var APIEnvelope response.Envelope = response.DefaultEnvelope{}

// main.go（release）
s.SetEnvelope(routes.APIEnvelope)

// main_doc.go（doc 构建）
openapi.Generate(out, routes.All(), openapi.OptionWithEnvelope(routes.APIEnvelope))
```

优先级：`RouteMeta.Envelope`（路由级，直接推导）> `OptionWithEnvelope`（全局实例）> `OptionWithEnvelopeSchema`（手写钩子函数：RawEnvelope 的 map 形态反射不出固定 key 时才用）> 默认壳推导。

### 2.6 路由级纯文档增强（DescribeRoute）

错误响应、响应头、OperationID 等**纯文档**信息不进路由表：注册在 main_doc.go（`-tags openapi` 才编译），release 二进制零内容。key = handler 函数引用（反射取「包.函数」，与中间件钩子同机制）。

```go
// main_doc.go
openapi.DescribeRoute(handlers.GetUser, openapi.RouteDoc{
	OperationID: "getUserById",
	Errors: []openapi.ErrorDecl{
		{Status: 404, Description: "用户不存在"}, // 响应体由壳推导 + code 默认值注入
	},
	ResponseHeaders: []openapi.HeaderDecl{
		{Name: "X-RateLimit-Remaining", Description: "剩余配额"},
	},
	// Hide: true,  // 从文档剔除（运行时照常服务）
	// Hook: func(op *openapi3.Operation) { /* 兜底改写，最后应用 */ },
})
```

规则：

- 同 key 重复注册 panic；注册了但路由树没匹配到 → 生成警告（注册不会静默失效）
- 错误响应体默认由壳推导（`ErrorDecl.Schema` 可整体覆盖）
- 应用顺序：中间件钩子先、`RouteDoc.Hook` 最后
- `Deprecated` 在 `RouteMeta`（运行时+文档共用层，文档生成 `deprecated: true`）
- 非模板路由补录用 `openapi.RegisterManualPath(path, item)`（与模板路由冲突时 panic）

### 2.7 文档与代码的一致性约定

生成器从类型与标签推导，无需手写 schema：

- Q 的 `query/path/header/form` 标签 → 参数（类型、描述、required、default 全部来自字段）
- B 非接口类型 → `application/json` 请求体（组件化 `$ref` 去重、递归类型防栈溢出）
- R：普通类型 → 壳内 data schema；`*FileStream` → binary；接口类型（Empty/any）→ 任意 JSON
- `DefaultStatusCode` → 成功响应码
- 注册过 `ParamBinder` 的参数类型 → schema 标注 string（HTTP 形态是原始串），配套 `RegisterParamBinderSchema` 可声明精确 schema
- `validate`/`binding` 约束标签 → schema 约束；`example` 标签 → 示例值；指针字段 → nullable
- 类型级 schema 覆盖：`RegisterTypeSchema[T]` / `RegisterTypeSchemaFunc[T]`（组件替换，$ref 结构不变；decimal、自定义 Marshaler 类型不再退化成 string）
- 跨包同名类型 → 组件名自动升级为「末段包名_类型名」（生成警告，引用同步）
- 重复路由（同 path+method）在生成期直接 panic，提前暴露注册错误

### 2.8 生成命令与 CI 建议

```bash
go run -tags openapi . -out openapi.yaml        # YAML
go run -tags openapi . -out openapi.json        # JSON
./build.sh -s                                    # 项目自带 build.sh 的 spec 命令
```

CI 建议（仓库自带 `.github/workflows/ci.yml` 已覆盖前两条）：

```bash
go test -tags openapi ./...                       # 文档生成器测试（普通 go test 跑不到）
go run -tags openapi . -out openapi.yaml
git diff --exit-code openapi.yaml                 # 文档漂移检查：改了路由没重新生成就挂
```

规范检查进 CI：把 `openapi.Generate` 换成 `openapi.GenerateStrict`（有警告即返回错误）。

### 2.9 在线文档

spec 是标准 OpenAPI 3.1，任意渲染器可用。example 用 scalar-go 起了 `/docs` 页面（读取 `openapi.yaml`）；生产环境建议用构建标签或中间件把文档端点隔离在内网/鉴权之后，也可以把 spec `go:embed` 进二进制避免依赖工作目录文件。

### 2.10 注释即文档（源码解析）

字段/结构体/handler 的**源码注释**可以直接成为文档描述——注释只存在于源码，release 二进制零参与。main_doc.go 一行开启：

```go
openapi.Generate(out, routes.All(), openapi.OptionWithSourceComments())
```

提取范围（只解析主模块包，第三方/标准库跳过）：

| 注释位置 | 映射到文档 |
|---|---|
| 字段上方注释 | query/header/path 参数与 body 字段的 description |
| 结构体上方注释 | components 组件 description |
| handler 函数上方注释 | Summary/Description 兜底（RouteMeta 未写时；首行 → Summary，其余 → Description，有注释则不再报 missing summary 警告） |

```go
// 用户
type User struct {
	// 用户ID，全局唯一
	ID string `json:"id"`
}

// 获取用户详情；找不到返回 404
func GetUser(ctx context.Context, req GetUserReq, _ any) (User, error) { ... }
```

**优先级**：`description` 标签 > 注释（内置解析器遇已有描述自动让位）。

**自定义解析语义**：注释里想携带更多结构（如示例值），注册解析器接管——拿到注释原文与字段 schema 引用，任意改写：

```go
// main_doc.go：约定 "// 描述 | 示例:值"
openapi.RegisterCommentParser(func(src string, sch *openapi3.SchemaRef) *openapi3.SchemaRef {
	parts := strings.SplitN(src, "|", 2)
	desc := strings.TrimSpace(parts[0])
	example := ""
	if len(parts) == 2 {
		example = strings.TrimPrefix(strings.TrimSpace(parts[1]), "示例:")
	}
	return openapi.DescribeSchema(sch, desc, example) // $ref 字段自动 AllOf 包装
})
```

规则：解析器全局唯一（重复注册 panic）；未开启 `OptionWithSourceComments` 时生成警告；不在注释里内置任何 DSL——约定由你定义。

---

## 3. 编写新框架适配器

### 3.0 什么时候需要自己写

- 想接 chi、fiber、net/http 增强路由等新框架
- 框架已有的 gin/echo 适配器满足不了（极少：扩展点已覆盖壳/校验/错误映射/装饰/绑定）
- 收益：**openapi 文档层完全不用动**（框架无关，消费的是 contract.Group 树），业务代码零改动

### 3.1 一个适配器包含什么

参照 `servergin`（最完整）/ `serverecho`（结构已对齐），建议按三文件划分：

| 文件 | 职责 | 大小参考 |
|---|---|---|
| `xxx.go` | `Server` 结构 + 全部扩展点 Setter + `Mount` 入口 | ~100 行 |
| `mount.go` | `mountGroup`/`mount` 请求管线、错误决策（`resolveError`/`bindError`/`bindFail`）、路径风格转换、`serveFile` | ~240 行 |
| `bind.go` | **只做"从请求取原始值"**：`bindQueryPath`/`bindFields`/`collectRawValues` | ~120 行 |

绑定元数据解析（`contract.ParseFields`）、标量/切片/指针/time 绑定（`contract.SetRaw/SetSliceValue`）、管线公用件（`contract.NewValue/CheckTarget/IsBodyMethod/CheckHandler/TransformIn/TransformOut`）**全部在 contract 层共享**，适配器不需要复制这些逻辑——这是与早期版本最大的区别。

### 3.2 步骤一：建立适配器包

```bash
mkdir serverchi
```

单模块仓库：直接在仓库根新建包目录，**不需要独立 go.mod、不需要单独打 tag**——跟随主模块统一版本发布。

- **包名跟随现有约定**：servergin 是 `package servergin`、serverecho 是 `package serverecho`（注意两者文档注释里的 `Package server` 是历史笔误，以 `package` 行为准）；新适配器建议 `package serverchi`，避免 import 歧义
- 依赖纪律：适配器包只 import contract + 目标框架本体，不 import openapi

### 3.3 步骤二：Server 配置与扩展点

照抄 `servergin/gin.go` 的字段集与 Setter，只把 gin 类型换成目标框架：

```go
type Server struct {
	middlewares []chi.HandlerFunc // 框架中间件类型
	validators  []validator.Func
	mapError    func(err error) (httpStatus, bizCode int)
	decorate    func(c *chi.Context, ctx context.Context) context.Context
	envelope    response.Envelope
	bindStatus  int // 绑定/校验失败的 HTTP 状态码（默认 200）
}

func New() *Server {
	s := &Server{}
	s.mapError = defaultErrorMapper
	s.decorate = func(c *chi.Context, ctx context.Context) context.Context {
		return contract.WithFramework(ctx, c) // 框架的注入点
	}
	s.envelope = response.DefaultEnvelope{}
	s.bindStatus = http.StatusOK
	return s
}
// Use / AddValidator / SetErrorMapper / SetContextDecorator /
// SetEnvelope / SetBindErrorStatus —— 与 servergin 逐字对齐
```

`bindFail` 也照抄：非 200 时业务 code 跟随状态码，与 `StatusError` 约定一致。

### 3.4 步骤三：Mount / mountGroup

```go
func (s *Server) Mount(r chi.Router, groups []*contract.Group) {
	if len(s.middlewares) > 0 {
		r.Use(s.middlewares...)
	}
	for _, grp := range groups {
		s.mountGroup(r, grp)
	}
}

func (s *Server) mountGroup(parent chi.Router, grp *contract.Group) {
	sub := parent.Route(grp.Prefix, nil) // ← 换成目标框架的子组机制
	for _, mw := range grp.Middlewares {
		if fn, ok := mw.(chi.HandlerFunc); ok {
			sub.Use(fn)
			continue
		}
		// ❗中间件类型不匹配必须 panic，不能静默忽略
		panic(fmt.Sprintf("serverchi: group %q middleware type %T is not chi.HandlerFunc", grp.Prefix, mw))
	}
	for _, rt := range grp.Routes {
		s.mount(sub, rt)
	}
	for _, child := range grp.Children {
		s.mountGroup(sub, child)
	}
}
```

### 3.5 步骤四：mount 请求管线（核心）

这是唯一有分量的部分。骨架（完整可运行实现直接对照 `servergin/mount.go`）：

```go
func (s *Server) mount(g chi.Router, r contract.Route) {
	// ❗挂载期签名校验：panic 信息带 method+path，别拖到反射调用期
	if err := contract.CheckHandler(r.Handler); err != nil {
		panic(fmt.Sprintf("serverchi: mount %s %s: invalid handler: %v", r.Method, r.Path, err))
	}
	h := reflect.ValueOf(r.Handler)
	qType, bType := h.Type().In(1), h.Type().In(2)

	env := s.envelope // 路由级 Envelope > 服务级
	if r.Envelope != nil {
		env = r.Envelope
	}
	successStatus := r.DefaultStatusCode // > 200
	if successStatus == 0 {
		successStatus = http.StatusOK
	}
	fail := func(c chi.Context, status, code int, msg string) {
		c.JSON(status, env.Failure(status, code, msg))
	}
	bindStatus, bindCode := bindFail(s.bindStatus)

	g.Handle(r.Method, chiPath(r.Path), func(c chi.Context) {
		// ❶ 装饰最先执行：TransformIn/校验器/TransformOut/handler 共享同一 ctx
		ctx := s.decorate(c, c.Request.Context())

		// ❷ Q：接口占位（NoReq/any）跳过绑定与校验
		qArg := reflect.New(qType).Elem()
		if qType.Kind() != reflect.Interface {
			q := contract.NewValue(qType)
			if err := bindQueryPath(c, q.Interface()); err != nil {
				st, cd, msg := s.bindError(err) // 绑定错误也尊重 StatusError（ParamBinder 可能返回 404）
				fail(c, st, cd, msg)
				return
			}
			if err := contract.TransformIn(ctx, q.Interface()); err != nil { // ❗Q 也要转
				st, cd, msg := s.bindError(err)
				fail(c, st, cd, msg)
				return
			}
			qArg = q.Elem()
		}

		// ❸ B：IsBodyMethod + 非接口 + 有 body；固定 JSON 解码
		bArg := reflect.New(bType).Elem()
		if contract.IsBodyMethod(r.Method) && bType.Kind() != reflect.Interface {
			b := contract.NewValue(bType)
			if c.Request().Body != nil && c.Request().ContentLength > 0 {
				if err := json.NewDecoder(c.Request().Body).Decode(b.Interface()); err != nil {
					st, cd, msg := s.bindError(err)
					fail(c, st, cd, msg)
					return
				}
				if err := contract.TransformIn(ctx, b.Interface()); err != nil {
					st, cd, msg := s.bindError(err)
					fail(c, st, cd, msg)
					return
				}
				bArg = b.Elem()
			}
		}

		// ❹ 校验
		if err := validator.Run(ctx, r.Method, contract.CheckTarget(qType, qArg), contract.CheckTarget(bType, bArg), s.validators...); err != nil {
			st, cd, msg := s.bindError(err)
			fail(c, st, cd, msg)
			return
		}

		// ❺ 调用 + 错误解析（StatusError → StatusCoder → mapError）
		out := h.Call([]reflect.Value{reflect.ValueOf(ctx), qArg, bArg})
		if ei := out[1].Interface(); ei != nil {
			status, code, msg := resolveError(s, ei.(error))
			fail(c, status, code, msg)
			return
		}

		// ❻ Response[R] 解包：Status / Headers / Cookies（用框架原生 API 写出）
		status := successStatus
		respVal := out[0]
		if w, ok := respVal.Interface().(contract.ResponseWrapper); ok {
			if w.ResponseStatus() != 0 {
				status = w.ResponseStatus()
			}
			for k, v := range w.ResponseHeaders() {
				c.SetHeader(k, v) // ← 框架 API
			}
			for _, cookie := range w.ResponseCookies() {
				c.SetCookie(cookie)
			}
			respVal = reflect.ValueOf(w.ResponseData())
		}

		// ❼ 出参转换
		tv, err := contract.TransformOut(ctx, respVal)
		if err != nil {
			status, code, msg := resolveError(s, err)
			fail(c, status, code, msg)
			return
		}
		respAny := tv.Interface()

		// ❽ FileStream：指针（nil→404）与值类型双形态；其余统一壳写出
		switch fv := respAny.(type) {
		case *contract.FileStream:
			if fv == nil {
				fail(c, http.StatusNotFound, http.StatusNotFound, "file not found")
				return
			}
			serveFile(c, fv)
			return
		case contract.FileStream:
			serveFile(c, &fv)
			return
		}
		c.JSON(status, env.Success(status, respAny))
	})
}
```

`resolveError` / `resolveErrorStatus` / `bindError` / `bindFail` / `serveFile` 从 `servergin/mount.go` 原样移植（只换写出 API）；`serveFile` 里注意 **Size<=0 走分块传输**（不要写死 Content-Length）。

### 3.6 步骤五：bind.go 只写"取值"

框架差异被压缩到"怎么取原始参数"：

```go
func bindQueryPath(c chi.Context, req any) error {
	rv := reflect.ValueOf(req)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return errors.New("invalid params type")
	}
	return bindFields(c, rv.Elem(), contract.ParseFields(rv.Elem().Type())) // 元数据共享
}

func bindFields(c chi.Context, e reflect.Value, metas []contract.FieldMeta) error {
	for _, m := range metas {
		f := e.Field(m.Index)
		if len(m.Children) > 0 { /* 内嵌递归，照抄 servergin/bind.go（含 CanSet 守卫） */ }
		if binder, ok := contract.BinderFor(f.Type()); ok { // ❗ParamBinder 钩子在最前
			src := collectRawValues(c, m)
			if len(src) == 0 {
				continue // 参数缺失保持零值，required 交给校验器
			}
			v, err := binder(src)
			if err != nil {
				return err
			}
			f.Set(reflect.ValueOf(v))
			continue
		}
		if m.Path != "" { /* contract.SetRaw(f, 框架路径参数, m.Path) */ }
		if m.Header != "" { /* contract.SetRaw(f, 框架请求头, m.Header) */ }
		if m.Query != "" || m.Form != "" {
			/* contract.SetSliceValue（多值）→ contract.SetRaw → default 标签兜底 */
		}
	}
	return nil
}

// collectRawValues：把 path/header/query 的原始值收集成 []string 供绑定器消费
// （照抄 servergin/bind.go，替换取值 API：c.Param / c.GetHeader / c.QueryArray）
```

解析失败一律 `contract.SetRaw` 返回的 `invalid %s` 错误——**不要静默吞掉**。

### 3.7 步骤六：测试对齐

把 servergin 的测试翻译到新框架，作为等价性验收（清单即规格）：

- [ ] 健康检查壳输出；path 绑定；`ErrNotFound` → 404
- [ ] `binding/validate required` 校验；`Validate()` 方法；Playground 接入
- [ ] `InTransform`（**Q 和 B 都要**）与 `OutTransform`
- [ ] `StatusError` / `%w` 包装穿透 / 自定义 `StatusCoder` / 普通错误 200+code7
- [ ] `DefaultStatusCode` 与 `Response[R].Status` 动态覆盖
- [ ] 自定义壳（服务级 + 路由级覆盖）；失败响应也走壳
- [ ] query/default/header/切片（重复+逗号）/指针/`time.Time` 绑定；不支持类型报错
- [ ] `RegisterParamBinder` 类型接管绑定 + StatusError 汇入错误链
- [ ] FileStream 三形态（指针 / 值 / Size 未知）
- [ ] 组中间件继承；中间件类型不匹配 panic；Handler 签名非法 panic
- [ ] 非 JSON Content-Type 的 body 也按 JSON 解码（与 gin 语义一致）
- [ ] `SetBindErrorStatus(400)` 切换绑定错误状态码

### 3.8 注意事项清单

1. **挂载期 `contract.CheckHandler` 必须先做**，panic 信息带 method+path——签名错误的反射 panic 信息极其晦涩
2. **body 固定 JSON 解码**，不要用框架自带的 `c.Bind()`——按 Content-Type 分发会让同一份路由表在不同适配器上行为漂移（echo 的教训）
3. **Q 的 `TransformIn` 不要漏**——历史上两个适配器都只转了 B
4. **两条失败路径不能混**：绑定/校验失败 → `bindError`（尊重 StatusError，否则 `bindStatus`）；Handler 业务错误 → `resolveError`（StatusError → StatusCoder → mapError）
5. **错误优先级顺序不能变**：`StatusError` → `StatusCoder` → `SetErrorMapper`；用 `errors.As` 保证 `%w` 链穿透
6. **接口占位判断**：Q/B 的 `Kind() == reflect.Interface` 时跳过绑定与校验（`NoReq`/`any`/`Empty` 都是 defined any，不能进 type switch 的接口 case）
7. **中间件断言失败必须 panic**（带组名和实际类型）——静默忽略等于中间件隐身
8. **FileStream 双形态**（指针 + 值类型）；nil → 404；`Size<=0` 分块输出
9. **路径风格转换**：路由树统一 OpenAPI 花括号 `{id}`，适配器负责转目标语法（gin/echo → `:id`，chi 原生支持 `{id}`，fiber → `:id`）
10. **内嵌结构体递归在 `IsExported` 之前**（`contract.ParseFields` 已处理；自写遍历时注意），nil 指针内嵌加 `CanSet` 守卫防反射 panic
11. **default 标签**：query/form 缺省时用 `contract.SetRaw` 填充（字段 `IsZero()` 判断），与文档 default 同步
12. **壳覆盖优先级**：`RouteMeta.Envelope` > `SetEnvelope`；**失败输出也走解析后的 env**
13. **装饰器在每个请求最前执行**（含校验失败请求）——文档注释要写明"保持轻量，重操作放中间件"
14. **默认装饰器注入 `contract.WithFramework`**——文档语义依赖它
15. **共享缓存别重复造**：`ParseFields`/`SetRaw`/`SetSliceValue`/`NewValue`/`CheckTarget`/`IsBodyMethod` 都在 contract 层，适配器只写取值函数
16. **依赖最小化**：适配器包只 import contract + 框架本体；文档层（kin-openapi）永远不出现在适配器包的 import 里

---

## 4. FAQ

**Q：想要纯 RESTful 风格（无壳、4xx 语义）？**
`s.SetEnvelope(response.RawEnvelope{})` + `s.SetErrorMapper(...)` + `s.SetBindErrorStatus(http.StatusBadRequest)` 三行切换；文档侧用 `OptionWithEnvelopeSchema` 配对。

**Q：release 二进制会不会带文档生成依赖？**
不会。`openapi` 包整体 `//go:build openapi` 隔离；`build.sh -r` 自动校验依赖链（发现 openapi3 直接 fail）。

**Q：中间件的鉴权标注怎么进文档？**
`openapi.RegisterMiddlewareDoc(middleware.Auth, hook)`——按函数引用注册，401/security 在钩子里声明。

**Q：想把 `?ids=1,2` 绑成自定义类型 / ID 直查实体？**
`contract.RegisterParamBinder`（见 1.4），错误返回 `contract.StatusBadRequest/NotFound` 会自动汇入统一错误链。

**Q：handler 里想拿 `*gin.Context`？**
返回 `contract.Framework(ctx)` 断言（默认装饰器已注入）；自定义装饰器时记得手动链上 `contract.WithFramework`。这是最后手段，优先用标签与 `Response[R]`。

**Q：脚手架生成的项目编译不过？**
确认 go.mod 里 oapi-hinge 版本 ≥ v0.2.0（单模块版本），重新执行 `go mod tidy`；仍不行请提 issue。

## 5. 测试

openapi 必须带构建标签：

```bash
# 默认构建（release 语义，不含 openapi 包）
go build ./... && go vet ./... && go test ./...

# 文档生成器（必须带 tag，否则测试不跑）
go test -tags openapi ./...

# 一键全量（build + vet + test）
./test.sh
```

测试文件命名约定：`server_test.go`（基础链路）、`feature_test.go`（能力）、`parambinder_test.go`（绑定器）、`bench_test.go`（基准）、`constraints_test.go`（约束映射）等，按领域一个文件。

CI（`.github/workflows/ci.yml`）随 push 到 main / alpha 触发，openapi 自动带 `-tags openapi`；`openapi.GenerateStrict` 可用于把警告升级为失败的严格检查。
