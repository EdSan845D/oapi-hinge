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

统一 Handler 模板（与 v0.1 兼容）：`func(ctx context.Context, Q[, B]) (R, error)`。无 body 方法允许省略 B 参数（2 参简式）。Q/B 用结构体标签声明来源（`path:` / `query:` / `header:` / `cookie:` / `form:` / `json`），支持 default、必填（binding/validate 双标签）、指针、切片、time.Time。

### 注解

| 注解 | 层级 | 说明 |
|---|---|---|
| `oapi:route` | 方法·必填 | `"<METHOD> <相对路径>"`，路径省略 = 组根 |
| `oapi:prefix` | 类型 | 组前缀 |
| `oapi:tag` | 类型/方法 | OpenAPI tag |
| `oapi:timeout` | 类型/方法 | 超时声明，文档派生 x-timeout |
| `oapi:status` / `oapi:deprecated` / `oapi:envelope` | 方法 | 成功码 / 弃用 / 命名响应壳 |
| `oapi:middleware` | 类型/方法 | 环绕中间件（`oapi:auth` / `oapi:limit` 为其历史别名，值统一进 Middleware 名单，按声明顺序执行）：`pkg.Func` 限定符可解析时——类型级 → 组级中间件（scoped `Group("", mws...)`），方法级 → 路由直挂（编译期校验）；其余 → 内核拦截器注册名（RegisterInterceptor 按名解析）；中间件名命中文档侧 securitySchemes → 自动推导 security + 401 |

### 代码生成

```bash
go run github.com/EdSan845D/oapi-hinge/cmd/hinge gen        # 生成
go run github.com/EdSan845D/oapi-hinge/cmd/hinge gen -check # CI 门禁：产物过期即失败
```

产物（按 `hinge.gen.yaml` 的 targets 按需生成）：

| 文件 | 内容 |
|---|---|
| `apigen/specs_gen.go` | 端点描述变量（hinge.Endpoint） |
| `apigen/binders_gen.go` | 类型化绑定器（按 Q/B 类型去重，请求期零反射） |
| `apigen/register_<t>_gen.go` | 各框架注册函数（模板发射；`emiters.<t>.template` 可换自定义模板接入新框架） |
| `apigen/all_gen.go` | `All` 聚合器 + `RegisterAll<Gin/Echo/HTTP>` |
| `<包>/hinge_gen_table.go` | `Enterpoint()` 守卫 + `Endpoints()` 路径↔函数对应表 |

生成期即做诊断：路径冲突、path 参数与 Q 字段一致性、策略未声明、multipart 字段缺 form 标签、双前缀笔误等。

### 程序化覆写：EntryPointConfig

gen.Run 同进程可注入程序化配置（generate.go），注解为主、代码为辅：

```go
gen.Config{
	// ...
	EntryPoints: []gen.EntryPointConfig{
		{
			Name:       "SystemEp",
			Midllwares: []any{middleware.Auth}, // 组级中间件（运行时值，自动分档发射）
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

hinge.RegisterInterceptor("BearerAuth", func(ctx context.Context, ep hinge.Endpoint, req hinge.RequestReader, s hinge.Sink, next func(context.Context) error) error {
	tok, _ := req.Header("Authorization")
	if !strings.HasPrefix(tok, "Bearer ") {
		s.WriteJSON(http.StatusUnauthorized, map[string]any{"code": 401, "data": nil, "msg": "missing bearer token"})
		return nil
	}
	return next(ctx)
})

apigen.RegisterAllGin(r.Group("/api"), k, apigen.All{
	SystemEp: eps.SystemEp{},
	UserEp:   eps.UserEp{Store: eps.NewUserStore()},
	FileEp:   eps.FileEp{},
})
```

echo / 原生 http 各有对称的 `RegisterAllEcho` / `RegisterAllHTTP`——同一份注解，换框架只改这一行。

## 包结构（单模块）

| 包 | 说明 |
|---|---|
| `hinge` | 运行时内核：Endpoint 契约、框架无关请求管线、错误链、响应壳、拦截器注册表（零反射） |
| `hinge/validator` | 自定义校验器扩展点 + go-playground 接入（可选依赖） |
| `gen` + `cmd/hinge` | 代码生成器：AST 注解解析 → IR → 绑定器/注册器/表发射 |
| `servergin` / `serverecho` / `serverhttp` | 薄 transport：取值 + 写出（约 300 行/框架） |
| `openapi` | OpenAPI 3.1 生成器，消费 `Endpoints()` 表（`//go:build openapi` 隔离，release 零开发依赖） |
| `scaffold` | 项目脚手架（`oapi-hinge create myapp`） |

## OpenAPI 文档

```go
//go:build openapi
// main_doc.go：go run -tags openapi . -out openapi.yaml
func collect(epss ...hinge.Enterpoint) []hinge.Endpoint {
	var out []hinge.Endpoint
	for _, ep := range epss {
		out = append(out, ep.Endpoints()...)
	}
	return out
}
```

`Endpoints()` 表即「路径↔函数对应关系」的唯一检视入口，conformance 测试与文档都从它派生。

### 中间件文档钩子

中间件对文档的影响（security、header 参数、错误响应等）通过函数引用注册钩子自定义。
生成器把每个端点的中间件引用发射为 `Endpoint.MWRefs`（全限定名，与反射派生函数名一致），
文档生成时按引用配对调用钩子：

```go
//go:build openapi
// main_doc.go
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

- **响应壳**：默认裸输出（RawEnvelope，不加包装器）；`k.SetEnvelope(hinge.DefaultEnvelope{})` 开启 `{code, data, msg}` 统一包装；`hinge.RegisterEnvelope(name, env)` + `oapi:envelope <name>` 路由级切换；文档侧 `OptionWithEnvelope` 从壳实例同构推导；
- **错误携带状态码**：`hinge.NotFound/BadRequest/...` 或实现 `StatusCoder`；默认 HTTP 200 + code=7，`k.SetBindErrorStatus(400)` 切 RESTful；
- **入参转换 / 出参加工**：`InTransform(ctx) error` / `OutTransform(ctx) error` 接口由生成绑定器与内核自动调用（零反射）；
- **校验器**：生成绑定器内置 required 检查 + `Validate()` 直调；`validator.Playground()` 接入完整规则（可选依赖）；
- **拦截器**：`RegisterInterceptor(name, fn)`，注解按名引用；短路时自行经 Sink 写出并返回 nil，返回错误走统一错误链；
- **中间件文档钩子**：`openapi.RegisterMiddlewareDoc(fn, hook)`（openapi tag），按函数引用为引用了该中间件的端点定制 security/参数/响应，见「中间件文档钩子」。

## 从 v0.1 迁移（破坏性变更）

| v0.1 | v0.2 |
|---|---|
| `contract.Group` 树 + `contract.New(RouteMeta[...])` | 删除；Enterpoint 结构体 + `oapi:*` 注解 |
| `servergin.New().Mount(...)` | `servergin.NewKernel()` + 生成的 `RegisterAllGin` |
| `Middlewares []any`（引擎类型断言） | `hinge.Interceptor`（框架无关，注解按名引用） |
| 文档钩子按函数名匹配 | 删除；文档语义来自注解，随函数走 |
| 反射绑定 + `RegisterParamBinder` | 生成绑定器（自定义解析请手写 Endpoint 逃生口） |
| `contract.Response[R]` | `hinge.Response[R]`；壳类型 `hinge.Reply[T]` |
| `contract.NotFound/...` | `hinge.NotFound/...` |

手写逃生口保留：直接构造 `hinge.Endpoint` + `Binder` + `HandlerFunc` 调 `Kernel.Handle`，即可在任意框架上挂载动态路由。

## License

MIT
