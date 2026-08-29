# oapi-hinge

一个 Go API 框架：**统一 Handler 模板 + 原生框架运行时 + OpenAPI 文档自动生成**。

本项目受 [go-fuego](https://github.com/go-fuego/fuego) 启发，文档生成基于 [kin-openapi](https://github.com/getkin/kin-openapi) 实现。

## 设计动机

业务 API 开发中，三个诉求经常打架：

1. **Handler 想写成纯函数**——不依赖具体 Web 框架，单测不起服务器；
2. **框架能力想全保留**——gin 的中间件生态、echo 的上下文特性，不想被抽象阉割；
3. **文档想自动生成**——类型即契约，OpenAPI 规范不该手写。

oapi-hinge 用「契约层描述 + 框架适配器执行 + 纯 kin-openapi 生成文档」三层结构同时满足三者。

📘 **使用手册**（快速开始 / OpenAPI 隔离构建 / 自定义适配器开发）：[docs/MANUAL.md](docs/MANUAL.md)

## 核心概念

### 统一 Handler 模板

所有业务接口遵循同一签名：

```go
func(ctx context.Context, query Q, body B) (resp R, error error)
```

- `Q`：query / path / header 参数，用结构体标签声明（`query:"page"`、`path:"id"`、`header:"X-Token"`）；`default:"2"` 声明缺省值（文档与运行时同步生效，支持基本类型、`time.Time`（RFC3339）、指针与切片）
- `B`：JSON 请求体（`any` 表示无 body）
- `R`：响应数据，自动包装为统一壳 `{code, data, msg}`
- 业务层零框架依赖，`context.Context` 用于取消/超时传播与用户注入

### 路由分组树

路由以树形分组声明，中间件随树继承：

```go
func All() []*contract.Group {
    return []*contract.Group{
        {
            Prefix: "/users", Tags: []string{"用户"},
            Middlewares: []any{middleware.Auth},
            Routes: []contract.Route{
                contract.New(contract.RouteMeta[handlers.ListUsersReq, any, response.Paged[handlers.User]]{
                    Method: "GET", Path: "", Summary: "用户列表",
                    Handler: handlers.ListUsers,
                }),
            },
        },
    }
}
```

运行时与文档生成器消费同一棵树，行为天然一致。

### 框架适配器（子模块）

| 子模块 | 说明 | 依赖 |
|---|---|---|
| `contract` | 核心契约：RouteMeta / Group / 响应壳 / 逃生舱 | 无（纯标准库） |
| `servergin` | gin 运行时适配器 | contract + gin |
| `serverecho` | echo 运行时适配器 | contract + echo |
| `openapi` | OpenAPI 3.1 文档生成器（开发期工具） | contract + kin-openapi |

按需引用子模块：用 gin 的项目不会拉到 echo，用 echo 的项目不会拉到 gin。

## 快速开始

```bash
# 使用脚手架生成项目（推荐）
go run github.com/EdSan845D/oapi-hinge/scaffold create myapp -m github.com/you/myapp

# 或在已有项目手动接入
go get github.com/EdSan845D/oapi-hinge/contract
go get github.com/EdSan845D/oapi-hinge/servergin
```

```go
package main

import (
    "context"
    "net/http"

    "github.com/EdSan845D/oapi-hinge/contract"
    "github.com/EdSan845D/oapi-hinge/servergin"
    "github.com/gin-gonic/gin"
)

type HealthReq struct{}

func Health(ctx context.Context, _ HealthReq, _ any) (map[string]string, error) {
    return map[string]string{"status": "ok"}, nil
}

func main() {
    r := gin.Default()
    s := servergin.New()
    s.Mount(r.Group("/api"), []*contract.Group{{
        Routes: []contract.Route{
            contract.New(contract.RouteMeta[HealthReq, any, map[string]string]{
                Method: "GET", Path: "/health", Summary: "健康检查",
                Handler: Health,
            }),
        },
    }})
    r.Run(":8080")
}
```

生成 OpenAPI 文档：

```go
// main_doc.go（构建标签 openapi）
openapi.Generate("openapi.yaml", routes.All(),
    openapi.OptionWithDocInfo(&openapi3.Info{Title: "myapp API", Version: "1.0.0"}),
    openapi.OptionWithServer(&openapi3.Servers{{URL: "/api"}}),
)
// 运行：go run -tags openapi . -out openapi.yaml
```

## 可插拔能力

### 响应壳自由定制

默认统一壳 `{code, data, msg}`；不想用壳时一行切换：

```go
s.SetEnvelope(response.RawEnvelope{}) // 成功裸输出 data，失败 {"error": msg}
```

或实现 `response.Envelope` 接口输出任意风格（RFC 9457、自定义协议等）；个别接口需不同壳时用 `RouteMeta.Envelope` 路由级覆盖。文档侧用 `openapi.OptionWithEnvelopeSchema(...)` 同步配置壳 schema。

### 错误携带状态码

```go
func GetUser(ctx context.Context, req GetUserReq, _ any) (User, error) {
    ...
    return User{}, contract.NotFound("用户不存在") // HTTP 404 + {"code":404,"msg":"用户不存在"}
}
```

便捷构造器：`BadRequest`/`Unauthorized`/`Forbidden`/`NotFound`/`Conflict`/`Internal`；自定义 error 类型只需实现 `StatusCoder` 接口即可携带状态码。非 200 错误与成功响应走同一套壳，格式始终一致。全局兜底仍可用 `SetErrorMapper`。

### 绑定/校验错误的 HTTP 状态码

参数绑定、校验失败默认返回 HTTP 200 + code=7（与业务错误同格式）；需要 RESTful 语义时一行切换：

```go
s.SetBindErrorStatus(http.StatusBadRequest) // 绑定/校验失败 → HTTP 400，业务 code 跟随状态码
```

Handler 返回的业务错误不受影响，仍按 StatusError / SetErrorMapper 解析。

### 成功状态码可声明

```go
contract.New(contract.RouteMeta[NoReq, CreateUserReq, User]{
    Method:            "POST",
    DefaultStatusCode: 201, // 文档与运行时同步生效
    Handler:           handlers.CreateUser,
})
```

动态覆盖优先级：`contract.Response[R]{Status}`（单次调用）> `DefaultStatusCode`（路由级）> 200。

### 入参转换 / 出参加工

```go
// 绑定后、校验前自动调用：trim 后能通过 required 必填检查
func (r *CreateUserReq) InTransform(ctx context.Context) error {
    r.Name = strings.TrimSpace(r.Name)
    return nil
}

// 序列化前自动调用：邮箱脱敏 alice@example.com -> a***@example.com
func (u *MaskedUser) OutTransform(ctx context.Context) error {
    if at := strings.Index(u.Email, "@"); at > 1 {
        u.Email = u.Email[:1] + "***" + u.Email[at:]
    }
    return nil
}
```

业务层只写纯函数，适配器自动触发，无需在 handler 里手动调用。

### 校验器扩展

内置必填标签双兼容（`binding:"required"` / `validate:"required"`）+ 结构体 `Validate()` 方法；需要完整规则时一行接入：

```go
s.AddValidator(validator.Playground()) // 支持 validate:"required,email,min=8" 等
```

不调用则完全不引入 go-playground 依赖。

## 框架特色

- **类型即契约**：Handler 的 Q/B/R 泛型参数直接驱动参数绑定与 OpenAPI schema 生成，业务层写一次，运行时和文档同时就绪；
- **框架可移植**：同一份路由注册表挂到 gin 或 echo 只差一行装配代码，业务代码零改动；
- **运行时零开发期依赖**：文档生成器带 `//go:build openapi` 标签，release 构建完全不包含 kin-openapi / yaml 等开发期依赖；
- **schema 自研反射生成**：组件化 `$ref` 去重、递归类型防栈溢出、`time.Time`/`[]byte`/泛型等开箱即用；
- **逃生舱体系**：遇到模板覆盖不了的场景，按优先级开逃生舱——`header` 标签绑定 → `contract.Response[R]` 响应定制（状态码/响应头/Cookie）→ `contract.WithFramework` 注入框架上下文；
- **中间件文档钩子**：中间件按函数名（反射派生）可选注册文档钩子（如鉴权中间件自动标注 BearerAuth），未注册钩子的中间件照常运行但不污染文档；
- **性能可控**：统一模板相对原生 gin Handler 的单请求开销约 0.8~1.9µs（反射调用），挂载期预计算 + 字段元数据缓存已将额外分配降到每次请求 4~6 个。


## License

MIT