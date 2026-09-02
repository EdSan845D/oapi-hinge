# oapi-hinge

[简体中文](README.md) | [English](README_EN.md)

A Go API framework where **endpoint functions + annotations are the entire route declaration — registration and docs are build artifacts**.

Inspired by [go-fuego](https://github.com/go-fuego/fuego) and [huma](https://github.com/danielgtaylor/huma), but goes one step further on the paradigm: instead of calling a registration function per endpoint, oapi-hinge removes that step entirely — you write endpoint methods with `oapi:*` annotations, and `hinge gen` produces route registration, typed binders, the Endpoints mapping table, and the OpenAPI document.

## Why

1. **The endpoint function is the single source of truth** — one route, one handler; its signature plus annotations already carry everything routing needs;
2. **Registration is the compiler's job** — generated registration code and typed binders call your methods directly; zero reflection at request time;
3. **Docs and runtime share one source** — both consume the same generated table, so they cannot drift apart;
4. **Framework portability is real** — gin / echo / net-http are thin transports (value extraction + response writing); business code and interceptors are framework-agnostic.

## Core concepts

### Enterpoint: the endpoint grouping unit

```go
// UserEp user endpoints.
//
// oapi:prefix /users
// oapi:tag users
// oapi:auth BearerAuth
type UserEp struct {
	Store *UserStore // fields = dependency container
}

// oapi:route GET
// List users (paged)
func (ep UserEp) ListUsers(ctx context.Context, q ListUsersReq) (hinge.Paged[User], error) {
	items, total := ep.Store.Page(q.Page, q.Size)
	return hinge.Paged[User]{Items: items, Total: total}, nil
}

// oapi:route GET /{id}
// Get user
func (ep UserEp) GetUser(ctx context.Context, q GetUserReq) (User, error) {
	if u, ok := ep.Store.Get(q.ID); ok {
		return u, nil
	}
	return User{}, hinge.NotFound("user not found")
}
```

Unified handler template: `func(ctx context.Context, Q[, B]) (R, error)`. Body-less methods may omit B. Q/B use struct tags for sources (`path:` / `query:` / `header:` / `cookie:` / `form:` / `json`), with defaults, required (binding/validate), pointers, slices, time.Time.

### Code generation

```bash
go run github.com/EdSan845D/oapi-hinge/cmd/hinge gen        # generate
go run github.com/EdSan845D/oapi-hinge/cmd/hinge gen -check # CI gate
```

Outputs per `hinge.gen.yaml`: endpoint specs, deduplicated typed binders, per-framework registration functions (`RegisterAllGin` / `RegisterAllEcho` / `RegisterAllHTTP`), and the `Endpoints()` mapping table. Diagnostics at generation time: route conflicts, path-param consistency, unregistered policies, multipart tag mistakes.

### Wiring: DI + one line

```go
k := servergin.NewKernel()
apigen.RegisterAllGin(r.Group("/api"), k, apigen.All{
	SystemEp: eps.SystemEp{},
	UserEp:   eps.UserEp{Store: eps.NewUserStore()},
})
```

## Packages

| Package | Purpose |
|---|---|
| `hinge` | Runtime kernel: Endpoint contract, framework-agnostic pipeline, error chain, envelopes, interceptor registry (zero reflection) |
| `gen` + `cmd/hinge` | Code generator: AST annotation parsing → IR → emitters |
| `servergin` / `serverecho` / `serverhttp` | Thin transports (~300 lines each) |
| `openapi` | OpenAPI 3.1 generator consuming `Endpoints()` tables (build-tag isolated) |
| `scaffold` | Project scaffolding (`oapi-hinge create myapp`) |

## Breaking changes from v0.1

`contract.Group` / `RouteMeta` registration, engine-typed middlewares, name-matched doc hooks, and the reflection-based binder registry are removed. See the Chinese README for the full migration table. A hand-written escape hatch (`hinge.Endpoint` + `Binder` + `Kernel.Handle`) remains for dynamic routes.

## License

MIT
