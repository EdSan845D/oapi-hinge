# oapi-hinge

[简体中文](README.md) | [English](README_EN.md)

一个 Go API 框架：**端点函数 + 注解即全部路由声明，注册与文档是构建产物**。

受 [go-fuego](https://github.com/go-fuego/fuego) 与 [huma](https://github.com/danielgtaylor/huma) 启发，但在范式上更进一步：fuego / huma 的路由注册仍是「逐端点调用注册函数」，oapi-hinge 把这一步整体省去——你只写端点方法和 `oapi:*` 注解，`hinge gen` 生成路由注册、类型化绑定器、Endpoints 对应表与 OpenAPI 文档。

## 设计动机

1. **端点函数是唯一事实源**——一个路由对应一个处理函数，中间件等横切语义都是它的补充；方法签名 + 注解已经包含了路由所需的全部信息，注册样板是重复誊写；
2. **注册应当是编译器的活**——生成的注册代码与强类型绑定器直调端点方法，请求期零反射；
3. **文档与运行时同源**——两者消费同一个生成表，不存在钩子失配导致的分叉；
4. **框架可移植是真的**——gin / echo / 原生 http 只是薄 transport（取值 + 写出），业务层与横切拦截器完全框架无关。

## 核心概念

### Enterpoint：端点分类单元

```go
// UserEp 用户端点。
//
// oapi:prefix /users
// oapi:tag 用户
// oapi:middleware BearerAuth
type UserEp struct {
	Store *UserStore // 字段 = 依赖容器，装配时注入
}

// oapi:route GET
// 用户列表（分页）
func (ep UserEp) ListUsers(ctx context.Context, q ListUsersReq) (hinge.Paged[User], error) {
	items, total := ep.Store.Page(q.Page, q.Size)
	return hinge.Paged[User]{Items: items, Total: total}, nil
}

// oapi:route GET /{id}
// 用户详情
func (ep UserEp) GetUser(ctx context.Context, q GetUserReq) (User, error) {
	if u, ok := ep.Store.Get(q.ID); ok {
		return u, nil
	}
	return User{}, hinge.NotFound("用户不存在")
}

// oapi:route POST
// oapi:status 201
// 创建用户
func (ep UserEp) CreateUser(ctx context.Context, _ any, b CreateUserReq) (User, error) {
	return ep.Store.Create(b.Name, b.Email), nil
}
```

统一 Handler 模板：`func(ctx context.Context, Q[, B]) (R, error)`。无 body 方法允许省略 B 参数（2 参简式）。Q/B 用结构体标签声明来源（`path:` / `query:` / `header:` / `cookie:` / `form:` / `json`），支持 default、必填（binding/validate 双标签）、指针、切片、time.Time。

### 注解

| 注解 | 层级 | 说明 |
|---|---|---|
| `oapi:route` | 方法·必填 | `"<METHOD> <相对路径>"`，路径省略 = 组根 |
| `oapi:prefix` | 类型 | 组前缀 |
| `oapi:tag` | 类型/方法 | OpenAPI tag |
| `oapi:timeout` | 类型/方法 | 超时声明，文档派生 x-timeout |
| `oapi:status` / `oapi:deprecated` / `oapi:envelope` | 方法 | 成功码 / 弃用 / 命名响应壳 |
| `oapi:middleware` | 类型/方法 | **框架原生中间件**（第三方框架通道，值为 `pkg.Func` 函数引用，编译期校验）：类型级 → 组级中间件（scoped `Group("", mws...)`），方法级 → 路由直挂；作用于框架链、内核之外；无端点上下文。中间件引用尾段名命中文档侧 securitySchemes → 自动推导 security + 401 |
| `oapi:interceptor` | 类型/方法 | **内核拦截器**（hinge.Interceptor 签名，值为 `pkg.Func` 函数引用，生成期签名校验）：类型级 → owner 全端点，方法级 → 本端点；发射为 `HandleWith` 的 extra 实参，进内核拦截链（correlation/timeout 之后、bind 之前），持端点上下文与统一错误链，跨框架可移植 |
| ~~`oapi:auth` / `oapi:limit`~~ | — | **已移除**（v0.2 二分：框架原生用 `oapi:middleware`，内核拦截器用 `oapi:interceptor`，值均为函数引用，不支持裸名） |

### 代码生成

```bash
go run github.com/EdSan845D/oapi-hinge/cmd/hinge gen        # 生成（纯注解项目）
go run github.com/EdSan845D/oapi-hinge/cmd/hinge gen -check # CI 门禁：产物过期即失败
```

> **程序化 EntryPoints 项目（generate.go 注入）**：CLI 生成会丢失 EntryPointConfig
>（组级中间件 / FuncDecls 覆写），已被禁止——产物头部带
> `entrypoints: programmatic` 标记，CLI 检测到即报错引导。生成与门禁走项目内入口：
> `go run ./app` 与 `go run ./app -check`（参考 example/app/generate.go）。

产物（按 `hinge.gen.yaml` 的 targets 按需生成）：

| 文件 | 内容 |
|---|---|
| `apigen/specs_gen.go` | 端点描述函数（hinge.Endpoint，按需构造，导入零分配）+ `All` 聚合器 |
| `apigen/binders_gen.go` | 类型化绑定器（按 Q/B 类型去重，请求期零反射） |
| `apigen/docs_gen.go` | 文档描述函数（hinge.EndpointDoc，按需构造；仅 `docs/` 文档入口消费） |
| `apigen/register_<t>_gen.go` | 各框架注册函数 + `RegisterAll<Target>`（模板发射；`emiters.<t>.template` 可换自定义模板接入新框架） |

生成期即做诊断：路径冲突、path 参数与 Q 字段一致性、策略未声明、multipart 字段缺 form 标签、双前缀笔误等。

### 程序化覆写：EntryPointConfig

gen.Run 同进程可注入程序化配置（generate.go），注解为主、代码为辅：

```go
gen.Config{
	// ...
	EntryPoints: []gen.EntryPointConfig{
		{
			Name: "SystemEp",
			Middlewares: []any{middleware.Auth},              // 组级框架原生中间件（组级直挂）
			Interceptors: []hinge.Interceptor{middleware.AccessLog}, // owner 全端点内核拦截器（extra）
			FuncDecls: map[gen.FuncId]gen.RouteMeta{
				gen.FuncIdentity(eps.SystemEp.Health): {
					Summary:     "健康检查（代码覆写示例）", // 字段级覆写：非零字段才覆盖注解值
					Deprecated:  gen.Ptr(true),           // 三态：nil 不动 / true 置位 / false 清除
				},
			},
		},
	},
}
```

FuncDecls 支持覆写 Summary / Description / Tags / DefaultStatusCode / Envelope / Deprecated（*bool 三态），
零值字段保持注解不变；命中端点在生成日志输出覆写提示（如 `注：SystemEp.Health 被 EntryPointConfig.FuncDecls 覆写：summary, deprecated=true`），
保证代码定义的覆写可见。路由（Method/Path）以注解为唯一事实源，不参与覆写。

### 装配：DI + 一行注册

```go
r := gin.Default()
k := servergin.NewKernel()
k.SetCorrelation(true)
k.AddValidator(validator.Playground())

// 拦截器无需注册：oapi:interceptor 注解 / EntryPointConfig.Interceptors
// 直接引用具名包级函数（如 app/middleware.BearerAuth），生成代码发射为
// HandleWith 的 extra 实参，进内核拦截链。

apigen.RegisterAllGin(r.Group("/api"), k, apigen.All{
	SystemEp: eps.SystemEp{},
	UserEp:   eps.UserEp{Store: eps.NewUserStore()},
	FileEp:   eps.FileEp{},
})
```

echo / 原生 http 各有对称的 `RegisterAllEcho` / `RegisterAllHTTP`——同一份注解，换框架只改这一行。

## 包结构（多模块：内核零框架依赖）

仓库为多模块布局：根模块只含内核（hinge）/ 生成器 / 文档，**go.mod 零框架依赖**；servergin / serverecho / serverhttp / validator 是独立子模块，各自携带自己的框架依赖（gin / echo / go-playground）——只 import 内核的项目不引入任何框架依赖。

| 包 | 说明 |
|---|---|
| `hinge`（根模块） | 运行时内核：Endpoint 契约、框架无关请求管线、错误链、响应壳（零反射；拦截器为直接函数引用，无注册表） |
| `servergin` | **独立子模块**（tag `servergin/vX.Y.Z`）：gin 适配器（自带 gin 依赖） |
| `serverecho` | **独立子模块**（tag `serverecho/vX.Y.Z`）：echo 适配器（自带 echo 依赖） |
| `serverhttp` | **独立子模块**（tag `serverhttp/vX.Y.Z`）：标准库 http 适配器（零第三方依赖） |
| `validator` | **独立子模块**（tag `validator/vX.Y.Z`）：go-playground 接入（可选依赖） |
| `gen` + `cmd/hinge` | 代码生成器：AST 注解解析 → IR → 绑定器/注册器/表发射 |
| `openapi` | OpenAPI 3.1 生成器，消费文档描述表（按需 import 即隔离，release 零开发依赖） |
| `scaffold` | 项目脚手架（`oapi-hinge create myapp`） |

## OpenAPI 文档

文档生成器按需 import 即隔离：只有 `docs/` 独立入口（example 与脚手架项目自带）import `openapi` 包，
运行时二进制（main.go）不引用即不链接任何文档依赖。

```go
// docs/main.go：go run ./docs -out openapi.yaml
if err := openapi.Generate(
	*out,
	apigen.AllDocSpecs(), // 文档描述表（docs_gen.go，hinge gen 生成、按需构造）
	openapi.OptionWithDocInfo(info),
	openapi.OptionWithServer(servers),
	openapi.OptionWithSourceComments(), // 注释即文档：字段/结构体注释进描述
); err != nil {
	panic(err)
}
```

`AllDocSpecs()` 即「端点→文档描述」的唯一检视入口，文档规范从它派生。

### 中间件文档钩子

中间件对文档的影响（security、header 参数、错误响应等）通过函数引用注册钩子自定义。
生成器把每个端点的中间件引用发射为 `Endpoint.MWRefs`（全限定名，与反射派生函数名一致），
文档生成时按引用配对调用钩子：

```go
// docs/main.go
openapi.RegisterMiddlewareDoc(middleware.Auth, func(op *openapi3.Operation) {
	op.Security = &openapi3.SecurityRequirements{{"BearerAuth": {}}}
	op.Responses.Set("401", &openapi3.ResponseRef{Value: openapi3.NewResponse().
		WithDescription("Unauthorized：缺少或无效的 Bearer token")})
})
openapi.RegisterMiddlewareDoc(middleware.ParseHeaderWithInfo, func(op *openapi3.Operation) {
	op.AddParameter(&openapi3.Parameter{Name: "X-SessionId", In: "header", Required: true,
		Description: "会话 ID", Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}})
	op.Responses.Set("403", &openapi3.ResponseRef{Value: openapi3.NewResponse().
		WithDescription("Forbidden：缺少 X-SessionId 请求头")})
})
```

无需手写中间件名字符串；未注册钩子的中间件不影响文档。内核拦截器注册名（无点形态）
另有内置推导：名字命中 `OptionWithSecurity` 注册的 scheme 时自动加 security + 401。

## 可插拔能力

- **响应壳**：默认裸输出（RawEnvelope，REST 风格）；`k.SetEnvelope(hinge.BizCodeEnvelope{OKCode: 0, ErrCode: 10000})` 开启 `{code, data, msg}` 统一包装（业务码取值由业务层配置，框架不内置业务码常量）；自定义壳只需实现 `Envelope` 接口——失败侧 `Failure(err) (status, body)` 拿到原始错误自行解释（`InspectError` 提取错误自带的状态码/业务码/明细），内核不做错误预解析；`hinge.RegisterEnvelope(name, env)` + `oapi:envelope <name>` 路由级切换；文档侧 `OptionWithEnvelope` 从壳实例同构推导；
- **错误携带状态码**：`hinge.NotFound/BadRequest/...` 或实现 `StatusCoder`；默认 HTTP 200 + code=7，`k.SetBindErrorStatus(400)` 切 RESTful；
- **入参转换 / 出参加工**：`InTransform(ctx) error` / `OutTransform(ctx) error` 接口由生成绑定器与内核自动调用（零反射）；
- **校验器**：生成绑定器内置 required 检查 + `Validate()` 直调；`validator.Playground()` 接入完整规则（可选依赖）；
- **拦截器**：`hinge.Interceptor` 具名包级函数，`oapi:interceptor` 注解 / `EntryPointConfig.Interceptors` 直接引用（无注册表）；短路时自行经 Sink 写出并返回 nil，返回错误走统一错误链；
- **中间件文档钩子**：`openapi.RegisterMiddlewareDoc(fn, hook)`，按函数引用为引用了该中间件的端点定制 security/参数/响应，见「中间件文档钩子」。

### 发布（多模块锁步）

多模块发版 = 每个模块一个带目录前缀的 tag（根 `vX.Y.Z`，子模块 `servergin/vX.Y.Z` …），已脚本化为一条命令：

```bash
./release.sh v0.2.0                  # 全量测试 → 子模块 go.mod 版本对齐 → 提交 → 5 tag → push
./release.sh v0.2.0 --skip-tests     # CI 已覆盖时跳过本地测试
```

脚本动作：工作区干净校验 → test.sh 全量测试 → 子模块 go.mod 内核依赖对齐到目标版本（幂等）→ 提交 → 根 + 4 个子模块同一提交打 tag → 推送。消费者按需升级（如 `go get github.com/EdSan845D/oapi-hinge/servergin@latest`），版本不同步不报错——适配器 go.mod 记录的内核版本即为兼容底线。

## 手写挂载

手写逃生口：直接构造 `hinge.Endpoint` + `Binder` + `HandlerFunc` 调 `Kernel.Handle`，即可在任意框架上挂载动态路由。

## License

MIT
