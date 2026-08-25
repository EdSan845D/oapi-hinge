# fuego-hinge

**统一 Handler 模板 + 原生 Gin 运行时 + fuego 仅作开发期 OpenAPI 文档生成器**

fuego-hinge 是一个 Go API 应用脚手架/框架，定位：

- **运行时纯净**：业务 API 跑在原生 Gin 上，Handler 零框架依赖，release 应用不包含任何开发期多余依赖
- **fuego 仅作开发期工具**：只在 `-tags openapi` 构建中充当 OpenAPI 文档生成器，release 二进制零 fuego
- **一处注册，双端消费**：`routes.All()` 同时驱动 Gin 挂载与 OpenAPI 文档生成，杜绝文档与代码不同步

## 依赖边界

| 类别 | 依赖 | 说明 |
|---|---|---|
| 运行时 | `gin` | release 二进制实际链接的唯一 Web 框架 |
| 开发期工具 | `fuego` / `kin-openapi` / `yaml.v3` | 仅 `-tags openapi` 构建使用；不出现在 release 二进制（build.sh -r 自动校验） |
| 业务层 | 无 | handlers 只依赖 `context` + 结构体 |

## 目录结构

```
framework/
├── main.go                  # 运行时入口（//go:build !openapi，原生 gin）
├── main_doc.go              # 开发期文档生成入口（//go:build openapi）
├── build.sh                 # release/dev/spec/test 一键构建
├── app/                     # ★ 业务层（日常开发改这里）
│   ├── handlers/            # 统一 Handler + 请求/响应结构体（含示例用户/文件业务）
│   └── routes/routes.go     # 路由注册表（唯一注册入口）
└── internal/                # 框架层（一般不用改）
    ├── contract/            # 核心契约：RouteMeta[Q,B,R]、NoReq/Empty/FileStream/ErrNotFound
    ├── response/            # 统一响应壳 {code, data, msg}
    ├── server/              # Gin 适配器：绑定/校验/响应/错误映射/中间件（扩展点）
    ├── validator/           # 校验器：标签必填 + Validate() 接口 + 自定义校验器
    └── openapi/             # 开发期文档生成（fuego 引擎，仅 openapi 标签参与构建）
```

## 统一 Handler 模板

```go
func(ctx context.Context, query Q, body B) (resp R, err error)
```

- **Q**：query 参数用 `query:"name"` 标签，路径参数用 `path:"name"` 标签；无入参用 `NoReq`
- **B**：POST/PUT/PATCH 传 body 结构体（整包 JSON 绑定）；无 body 传 `any`
- **R**：`*FileStream` 输出二进制流（io.Reader 数据源）；`Empty` 输出 `data: null`；其余走统一壳
- 错误：`ErrNotFound` → HTTP 404；其他业务错误 → HTTP 200 + `code:7`（可用 `SetErrorMapper` 自定义）

## 新增一个接口（3 步）

```go
// 1. app/handlers/xxx.go：写统一 Handler
type GetUserReq struct {
    ID string `path:"id" description:"用户ID"`
}

func GetUser(ctx context.Context, req GetUserReq, _ any) (User, error) {
    // ... 业务逻辑
    return User{}, ErrNotFound // 资源不存在
}

// 2. app/routes/routes.go：注册一行
contract.New(contract.RouteMeta[GetUserReq, any, User]{
    Method: "GET", Path: "/users/{id}", Summary: "用户详情",
    Tags: []string{"用户"}, Auth: true, Handler: GetUser,
}),
```

3. 完成 —— 运行时路由与 OpenAPI 文档同时生效。

## 参数校验（扩展点）

| 层级 | 方式 | 说明 |
|---|---|---|
| 1 标签 | `binding:"required"` | 绑定阶段自动校验缺失 |
| 2 接口 | 结构体实现 `Validate() error` | 绑定后自动调用（见 `CreateUserReq` 示例） |
| 3 自定义 | `server.AddValidator(func(ctx, method, q, b) error)` | 全局追加校验器 |
| 4 中间件 | `server.Use(gin.HandlerFunc...)` | 校验之前的全局中间件（鉴权/CORS/限流） |

## 构建与运行

> 环境要求：Go ≥ 1.26.5（fuego v0.20.0 的 go.mod 要求；本地 Go 较旧时 `go` 会自动下载匹配工具链）

```bash
# 运行（dev 模式跳过示例鉴权）
FUEGO_HINGE_ENV=dev go run .
# 或
./build.sh -d && FUEGO_HINGE_ENV=dev ./bin/app-dev

# release：默认标签构建 + 自动检查依赖链无 fuego
./build.sh -r

# 生成 OpenAPI 文档（开发期工具）
./build.sh -s          # 等价于 go run -tags openapi . -out openapi.yaml

# 测试
./build.sh -t
```

验证 release 隔离：

```bash
./build.sh -r
# 输出应为：
# --- release 依赖链检查（应无 fuego）---
# OK: release 构建不包含 fuego
```

生成后的 `openapi.yaml` 可直接导入 Swagger UI / Apifox / Postman。

## 示例接口（开箱即用）

| 方法 | 路径 | 说明 | 演示点 |
|---|---|---|---|
| GET | /api/health | 健康检查 | 无参 + 无鉴权 |
| GET | /api/users | 用户列表 | query 分页 + 默认值 |
| GET | /api/users/{id} | 用户详情 | path 参数 + 404 |
| POST | /api/users | 创建用户 | JSON body + 标签必填 + Validate() |
| DELETE | /api/users/{id} | 删除用户 | Empty 响应 |
| GET | /api/files/{name} | 下载文件 | FileStream 二进制流（go:embed） |

## 修改指南

- **换模块名**：全局替换 `fuego-hinge`（go.mod module 名 + import 路径）；用 `scaffold` CLI 生成新项目会自动完成
- **写业务**：改 `app/handlers/` + `app/routes/routes.go`，删掉示例 user/file 即可
- **换鉴权**：改 `main.go` 里的示例 token 中间件（JWT/OAuth 在此接入）；`Auth: true` 控制文档标注
- **改响应语义**：`internal/response`（壳结构）；`server.SetErrorMapper`（错误码/HTTP 状态）
- **改文档信息**：`main_doc.go` 的 `DocInfo`（标题/版本/描述）
- **加依赖**：直接 `go get`；注意 release 构建不得 import `internal/openapi`（构建标签隔离，build.sh -r 会自动检查）

## 设计要点

- **构建隔离**：`main.go`/`internal/server` 等运行时文件带 `//go:build !openapi`，`main_doc.go`/`internal/openapi` 带 `//go:build openapi`，两套互斥；release 构建默认标签，fuego 不进依赖链
- **业务零框架**：Handler 只依赖 context + 结构体；gin 只在 `main.go` 与 `internal/server` 出现
- **一处注册，双端消费**：`routes.All()` 同时驱动 gin 挂载与 OpenAPI 生成，杜绝"文档与代码不同步"
- **已知限制**：递归 schema（自引用结构体）会触发 fuego v0.20.0 的 ref 解析栈溢出，生成器已绕过 `OutputOpenAPISpec()` 直接序列化；文档版本为 OpenAPI 3.1
