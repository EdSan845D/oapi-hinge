# oapi-hinge

[简体中文](README.md) | [English](README_EN.md)

A Go API framework: **unified handler signature + native framework runtimes + automatic OpenAPI documentation**.

Inspired by [go-fuego](https://github.com/go-fuego/fuego); documentation generation is built on [kin-openapi](https://github.com/getkin/kin-openapi).

## Design Motivation

In day-to-day API development, three goals often pull against each other:

1. **Handlers should be pure functions**—no dependency on a specific web framework; unit tests call the function directly, no HTTP server required;
2. **Framework power should stay intact**—gin's middleware ecosystem, echo's context features, with no abstraction tax;
3. **Docs should be generated automatically**—types are the contract; OpenAPI specs shouldn't be handwritten.

oapi-hinge satisfies all three with a three-layer design: **a contract layer for description, framework adapters for execution, and pure kin-openapi for documentation**.

📘 **User manual** (quick start / isolated OpenAPI builds / custom adapter development): [docs/MANUAL_EN.md](docs/MANUAL_EN.md)

## Core Concepts

### Unified Handler Signature

Every business endpoint follows the same signature:

```go
func(ctx context.Context, query Q, body B) (resp R, error error)
```

- `Q`: query / path / header parameters declared with struct tags (`query:"page"`, `path:"id"`, `header:"X-Token"`); `default:"2"` declares fallback values (effective in both docs and runtime; supports basic types, `time.Time` (RFC3339), pointers, and slices)
- `B`: JSON request body (`any` means no body)
- `R`: response data, automatically wrapped in the unified envelope `{code, data, msg}`
- The business layer has zero framework dependencies; `context.Context` is used for cancellation/timeout propagation and user-injected values

### Route Group Tree

Routes are declared as a tree of groups; middleware is inherited down the tree:

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

The runtime and the documentation generator consume the same tree, so behavior stays consistent by construction.

### Package Layout (single module)

The whole framework is a single Go module `github.com/EdSan845D/oapi-hinge`; one version covers all packages:

| Package | Purpose | Dependencies |
|---|---|---|
| `contract` | Core contract: RouteMeta / Group / response envelope / error types / binding helpers / extension-point registry | no third-party deps |
| `servergin` | gin runtime adapter | gin |
| `serverecho` | echo runtime adapter | echo |
| `openapi` | OpenAPI 3.1 documentation generator (dev-time tool, isolated by the `//go:build openapi` tag) | kin-openapi |
| `scaffold` | project scaffold CLI | no third-party deps |

Release builds contain none of the documentation-generation dependencies (the `openapi` package is fully isolated behind a build tag).

## Quick Start

```bash
# Generate a project with the scaffold (recommended)
go run github.com/EdSan845D/oapi-hinge/scaffold@latest create myapp -m github.com/you/myapp

# Or wire it into an existing project manually
go get github.com/EdSan845D/oapi-hinge
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
                Method: "GET", Path: "/health", Summary: "Health check",
                Handler: Health,
            }),
        },
    }})
    r.Run(":8080")
}
```

Generating OpenAPI documentation:

```go
// main_doc.go (build tag: openapi)
openapi.Generate("openapi.yaml", routes.All(),
    openapi.OptionWithDocInfo(&openapi3.Info{Title: "myapp API", Version: "1.0.0"}),
    openapi.OptionWithServer(&openapi3.Servers{{URL: "/api"}}),
)
// Run: go run -tags openapi . -out openapi.yaml
```

## Pluggable Capabilities

### Custom Response Envelopes

The default unified envelope is `{code, data, msg}`; switching it off takes one line:

```go
s.SetEnvelope(response.RawEnvelope{}) // success returns raw data; failure returns {"error": msg}
```

Or implement the `response.Envelope` interface for any style you like (RFC 9457, custom protocols, etc.); for per-route differences use the `RouteMeta.Envelope` override. On the documentation side, configure the envelope schema with `openapi.OptionWithEnvelopeSchema(...)`.

### Errors That Carry HTTP Status Codes

```go
func GetUser(ctx context.Context, req GetUserReq, _ any) (User, error) {
    ...
    return User{}, contract.NotFound("user not found") // HTTP 404 + {"code":404,"msg":"user not found"}
}
```

Convenience constructors: `BadRequest`/`Unauthorized`/`Forbidden`/`NotFound`/`Conflict`/`Internal`; custom error types only need to implement the `StatusCoder` interface to carry a status code. Non-200 errors and successful responses share the same envelope, so the format is always consistent. A global fallback is still available via `SetErrorMapper`.

### HTTP Status for Binding/Validation Errors

Parameter binding and validation failures return HTTP 200 + code=7 by default (same format as business errors); switch to RESTful semantics with one line:

```go
s.SetBindErrorStatus(http.StatusBadRequest) // binding/validation failures → HTTP 400, business code follows the status
```

Business errors returned by handlers are unaffected and are still resolved via StatusError / SetErrorMapper.

### Declarable Success Status Codes

```go
contract.New(contract.RouteMeta[NoReq, CreateUserReq, User]{
    Method:            "POST",
    DefaultStatusCode: 201, // effective in both docs and runtime
    Handler:           handlers.CreateUser,
})
```

Dynamic override precedence: `contract.Response[R]{Status}` (single call) > `DefaultStatusCode` (route level) > 200.

### Request Transforms / Response Transforms

```go
// Called automatically after binding and before validation: the trimmed value passes the required check
func (r *CreateUserReq) InTransform(ctx context.Context) error {
    r.Name = strings.TrimSpace(r.Name)
    return nil
}

// Called automatically before serialization: email masking alice@example.com -> a***@example.com
func (u *MaskedUser) OutTransform(ctx context.Context) error {
    if at := strings.Index(u.Email, "@"); at > 1 {
        u.Email = u.Email[:1] + "***" + u.Email[at:]
    }
    return nil
}
```

The business layer only writes pure functions; adapters trigger the hooks automatically—no manual calls inside handlers.

### Validator Extension

Built-in dual compatibility for required tags (`binding:"required"` / `validate:"required"`) plus struct `Validate()` methods; for full rule sets, plug one in with a single line:

```go
s.AddValidator(validator.Playground()) // supports validate:"required,email,min=8" etc.
```

Without this call, the go-playground dependency is never pulled in.

## Framework Highlights

- **Types are the contract**: the Q/B/R generic parameters of a handler directly drive parameter binding and OpenAPI schema generation—write the business layer once, and both runtime and docs are ready;
- **Framework portability**: mounting the same route registry onto gin or echo differs by one line of assembly code, with zero changes to business code;
- **Zero dev-time dependencies at runtime**: the documentation generator carries the `//go:build openapi` tag, so release builds contain none of the dev-time dependencies such as kin-openapi / yaml;
- **Self-built reflective schema generation**: componentized `$ref` deduplication, recursion guards against stack overflow, and out-of-the-box support for `time.Time`/`[]byte`/generics;
- **Graduated customization**: for cases the template can't cover, loosen constraints step by step by priority—`header` tag binding → `contract.Response[R]` response customization (status/headers/cookies) → `contract.WithFramework` framework-context injection;
- **Middleware documentation hooks**: middleware can optionally register documentation hooks by function name (reflection-derived)—e.g. an auth middleware automatically annotated with BearerAuth; middleware without a registered hook still runs normally but stays out of your docs;
- **Measurable performance overhead**: the unified template adds roughly 0.8~1.9µs per request over a native gin handler (reflective call); mount-time precomputation plus field metadata caching keeps the extra allocations down to 4~6 per request.


## License

MIT
