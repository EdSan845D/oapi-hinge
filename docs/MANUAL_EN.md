# oapi-hinge User Manual

[简体中文](MANUAL.md) | [English](MANUAL_EN.md)

> Applies to: V0.2.0 <br/>
> For design motivation and architecture overview, see the [README](../README_EN.md).

## Table of Contents

- [0. Core Concepts at a Glance](#0-core-concepts-at-a-glance)
- [1. Quick Start (servergin)](#1-quick-start-servergin)
- [2. OpenAPI Documentation: Build-Time Isolation](#2-openapi-documentation-build-time-isolation)
- [3. Writing a New Framework Adapter](#3-writing-a-new-framework-adapter)
- [4. FAQ](#4-faq)

---

## 0. Core Concepts at a Glance

### Three-Layer Structure

| Layer | Package | Responsibility | Dependencies |
|---|---|---|---|
| Contract layer | `contract` | Handler signature, route group tree, response envelope, error types, binding helpers, extension-point registry | no third-party deps |
| Runtime layer | `servergin` / `serverecho` / custom | Mount the route tree onto a concrete framework and run the request pipeline | the corresponding framework |
| Documentation layer | `openapi` | Generate OpenAPI 3.1 from the same route tree (`-tags openapi` isolated build) | kin-openapi |

Single module `github.com/EdSan845D/oapi-hinge`; one version covers all packages. Release builds contain none of the documentation-generation dependencies (the `openapi` package is fully isolated behind `//go:build openapi`).

### Unified Handler Signature

Every business endpoint is the same pure-function signature:

```go
func(ctx context.Context, q Q, b B) (r R, err error)
```

- `Q`: query / path / header parameter struct (declared with tags)
- `B`: JSON request body (`any` or the `contract.NoReq` placeholder means no body)
- `R`: response data (automatically wrapped in the response envelope; `any` / `contract.Empty` outputs `data: null`)

### Full Request Pipeline

```
Run middleware (global + inherited from groups) ↓ (the chain below can be seen as handler wrappers; middleware execution order stays unchanged)
  → Context decoration decorate (runs first, shared across the whole chain)
  → Q binding (query/path/header/default) → Q.InTransform
  → B binding (POST/PUT/PATCH, strict JSON) → B.InTransform
  → Validation (required tags → Validate() method → validators registered via AddValidator)
  → Handler call (reflection, precomputed at mount time)
  → err? → Error resolution (StatusError → StatusCoder → SetErrorMapper) → envelope failure output
  → contract.Response[R] unwrap (Status/Headers/Cookies)
  → R.OutTransform
  → FileStream? → stream output
  → Envelope success output → JSON
```

---

## 1. Quick Start (servergin)

> This section uses gin as the example. **Other frameworks work the same way**: swap the adapter and change nothing else in your business code (see [1.11](#111-other-frameworks-work-the-same-way)).

### 1.1 Installation

```bash
go get github.com/EdSan845D/oapi-hinge
```

### 1.2 Minimal Runnable Project

```go
package main

import (
	"context"
	"net/http"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/servergin"
	"github.com/gin-gonic/gin"
)

// ---- Q: GET /api/hello?name=xxx ----
type HelloReq struct {
	Name string `query:"name" binding:"required" description:"称呼"`
	// Greetings
	Greetings string `query:"greetings" default:"how's it going?"`
}

// ---- Handler: unified pure-function template ----
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

After it runs: `curl 'http://localhost:8080/api/hello?name=Cara'` →

```json
{"code":0,"data":{"msg":"hello Cara, how's it going?"},"msg":"操作成功"}
```

The `RouteMeta[Q, B, R]` generic parameters are exactly the handler's three inputs / return type, and consistency is checked at compile time. Full field list:

| Field | Description |
|---|---|
| `Method` | HTTP method (`GET`/`POST`/`PUT`/`PATCH`/`DELETE`…) |
| `Path` | OpenAPI-style path such as `/users/{id}` (auto-converted to gin's `:id`); relative within the group; use `""` for list routes |
| `Summary` / `Description` | Documentation summary / description |
| `Tags` | Documentation tags (merged and deduplicated with the group's Tags) |
| `DefaultStatusCode` | Declared success status code (effective in docs and runtime), defaults to 200 |
| `Envelope` | Route-level response envelope override (defaults to the service-level one) |
| `Handler` | Unified template function |

### 1.3 Organizing a Real Project

For real projects, layer your code around "handlers + a single route registry" (see the runnable example in [`example/`](../example/)):

```
app/
├── handlers/        # Unified handlers: func(ctx, Q, B) (R, error), pure functions
├── middleware/      # Business middleware (gin.HandlerFunc / echo.MiddlewareFunc)
└── routes/routes.go # Route registry: All() returns the whole contract.Group tree (the single registration entry)
```

Both the runtime and the documentation generator take their data from `routes.All()`—**register once, and both runtime and docs are ready**.

The group tree supports nested inheritance:

```go
func All() []*contract.Group {
	return []*contract.Group{
		{ // root group: no prefix
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[handlers.NoReq, any, map[string]string]{
					Method: "GET", Path: "/health", Summary: "健康检查", Handler: handlers.Health,
				}),
			},
		},
		{ // /users group: middleware inherits down to child groups; tags merge automatically
			Prefix:      "/users",
			Description: "用户相关接口",
			Tags:        []string{"用户"},
			Middlewares: []any{middleware.Auth},
			Routes: []contract.Route{ /* ... */ },
			Children: []*contract.Group{ /* child groups such as /users/admin */ },
		},
	}
}
```

### 1.4 Declaring Q: query / path / header Parameters

```go
type ListUsersReq struct {
	Page    int       `query:"page" default:"1" description:"页码"`
	Size    int       `query:"size" default:"10"`
	ID      int       `path:"id"`                          // route written as /users/{id}
	Token   string    `header:"X-Token"`                   // request header
	Keyword string    `query:"keyword" binding:"required"` // required
	Created time.Time `query:"created"`                    // RFC3339, e.g. 2026-08-28T00:00:00Z
}
```

| Tag | Effect | Documentation sync |
|---|---|---|
| `query:"page"` | query parameter | ✅ generates a query parameter |
| `path:"id"` | path parameter (type/description taken from the field) | ✅ generates a path parameter |
| `header:"X-Token"` | request header | ✅ generates a header parameter |
| `form:"f"` | equivalent to query | ✅ |
| `default:"1"` | **runtime** fallback value (filled in when absent) | ✅ generates default too |
| `description:"..."` | field description | ✅ |
| `example:"u123"` | example value (converted to the field's type; numbers/bool auto-converted) | ✅ generates example |
| `validate:"oneof=..."` / `min` / `max` / `gte` / `lte` / `email` / `url` | **constraints as documentation**: validation tags become schema constraints (enum / minLength / minimum / format…); `binding` tags participate equally | ✅ |
| `binding:"required"` / `validate:"required"` | required (zero value counts as missing) | ✅ generates required |

**Supported types**: string, integers/unsigned of all widths, floats, bool, `time.Time` (RFC3339), pointers (`*int` defaults to nil, so "absent" is distinguishable), and slices.

**Slice semantics**: repeated parameters `?ids=1&ids=2` and comma-joined values `?ids=1,2` are equivalent → `[]int{1,2}`; `[]string` only accepts repeated parameters (no comma splitting, raw values preserved).

**Embedded structs** are flattened automatically (including embedding of unexported types, e.g. `type Req struct { Pager; Name string }`), and tags still apply.

**Custom type binding (ParamBinder)**: any business type can register a binder that maps "raw strings → field value"—useful for comma strings → named slices, or ID → cached entity lookups:

```go
type IDs []int64

func init() {
	contract.RegisterParamBinder(func(src []string) (IDs, error) {
		// src: raw values of repeated parameters (?ids=1&ids=2 → ["1","2"]; split commas yourself)
		var out IDs
		for _, s := range src {
			for _, part := range strings.Split(s, ",") {
				v, err := strconv.ParseInt(part, 10, 64)
				if err != nil {
					return nil, contract.BadRequest("invalid id: " + part) // joins the unified error chain
				}
				out = append(out, v)
			}
		}
		return out, nil
	})
}
```

Once registered, fields of that type go through the binder automatically (path/query/form/header all work); when the parameter is missing the field keeps its zero value (required is left to the validator); in OpenAPI the parameter's schema is automatically annotated as string.

### 1.5 Declaring B: JSON Request Body

```go
type CreateUserReq struct {
	Name  string `json:"name" binding:"required"`
	Email string `json:"email" validate:"required,email"` // works with the Playground validator
}

func (r *CreateUserReq) InTransform(ctx context.Context) error { // optional: normalize automatically after binding
	r.Name = strings.TrimSpace(r.Name)
	return nil
}
```

- Only `POST` / `PUT` / `PATCH` parse the body; `B = any` (or an interface type) means no body—binding and validation are skipped
- JSON decoding is fixed (`Content-Type` does not change behavior; gin/echo semantics are identical)
- When `Content-Length = 0` the zero value is kept (the body may be omitted)

### 1.6 Declaring R: Responses and Status Codes

**Ordinary values** → the unified envelope `{code, data, msg}`:

```go
func GetUser(...) (User, error) { return u, nil }
// → 200 {"code":0,"data":{...},"msg":"操作成功"}
```

**No data** → `contract.Empty` (or `any`), which outputs `data: null`.

**Declaring a success status code** (effective in docs as well):

```go
contract.New(contract.RouteMeta[contract.NoReq, CreateUserReq, User]{
	Method: "POST", DefaultStatusCode: 201, Handler: CreateUser,
})
```

**Dynamic return values**: when you need per-call overrides of status code / headers / cookies, return `contract.Response[R]`:

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

Precedence: `Response[R].Status` (single call) > `DefaultStatusCode` (route level) > 200.

**Binary downloads**: return `*contract.FileStream` (the value type `contract.FileStream` works too):

```go
func Download(ctx context.Context, req DownloadReq, _ any) (*contract.FileStream, error) {
	f, err := os.Open(req.Name)
	if err != nil {
		return nil, contract.NotFound("文件不存在")
	}
	st, _ := f.Stat()
	return &contract.FileStream{
		Name:        filepath.Base(req.Name), // file name for Content-Disposition
		Size:        st.Size(),               // <=0 means chunked transfer
		ContentType: "application/pdf",
		Reader:      f,
	}, nil
}
```

The data source can be a file, `go:embed` in-memory data, or any `io.Reader`.

### 1.7 Error Handling

```go
return User{}, contract.NotFound("用户不存在")
// → HTTP 404 {"code":404,"data":null,"msg":"用户不存在"}
```

Convenience constructors: `BadRequest`(400) / `Unauthorized`(401) / `Forbidden`(403) / `NotFound`(404) / `Conflict`(409) / `Internal`(500).

| Scenario | HTTP | Business code | msg |
|---|---|---|---|
| Success | the route's declared success code (default 200) | 0 | SuccessMsg (default `"操作成功"`) |
| Ordinary business error | 200 (legacy default) | 7 | `err.Error()` |
| `StatusError` | its own status | `Code`, defaults to following the status | `Msg`, defaults to `err.Error()` |
| Custom `StatusCoder` | `StatusCode()` | 7 | `err.Error()` |
| Binding/validation failure | `bindStatus` (default 200) | 7 (follows the status when non-200) | error message |

Key points:

- **Error chains are unwrapped correctly**: wrapped errors like `fmt.Errorf("db: %w", contract.Forbidden("无权限"))` are still recognized (`errors.As`)
- **Internal causes never leak**: `contract.WithCause(statusErr, innerErr)` attaches details to the error chain (visible in logs); only `Msg` is exposed externally
- **Global fallback**: `s.SetErrorMapper(func(err) (httpStatus, bizCode))` only applies to ordinary errors that carry no status code
- **Going RESTful**: `s.SetBindErrorStatus(http.StatusBadRequest)` makes binding/validation failures return 400 (the default 200 + code 7 preserves legacy behavior); combine with `s.SetEnvelope(response.RawEnvelope{})` to switch to raw-output style entirely
- Custom error types only need to implement the `StatusCoder` interface (`error + StatusCode() int`) to carry a status code

### 1.8 Validation

Validation timing: after Q/B binding and `InTransform`, before the handler. Executed in order:

1. **Built-in required tags**: `binding:"required"` / `validate:"required"` (zero value counts as missing, recursing into embedded structs)
2. **The struct's own `Validate() error` method**
3. **Custom validators registered via `AddValidator`** (in registration order)

```go
s := servergin.New()

// When the built-in rules are enough: add nothing at all
// For full rules (email/min/oneof/custom tags…), plug in go-playground with one line:
s.AddValidator(validator.Playground()) // supports validate:"required,email,min=8"

// Custom validator: receives the method plus the parsed Q/B
s.AddValidator(func(ctx context.Context, method string, q, b any) error {
	if req, ok := b.(*CreateUserReq); ok && req.Name == "admin" {
		return contract.Forbidden("reserved name")
	}
	return nil
})
```

Without calling `Playground()`, go-playground is never compiled into your binary. Note that the built-in required check is a "zero-value check" (empty slices pass); for full go-playground semantics (empty slices count as missing), use Playground.

### 1.9 Request Transforms / Response Transforms

Just implement the interfaces; the adapter calls them automatically—no manual handling inside handlers:

```go
// After Q/B binding, before validation: the trimmed value passes the required check
func (r *CreateUserReq) InTransform(ctx context.Context) error {
	r.Name = strings.TrimSpace(r.Name)
	r.Email = strings.ToLower(r.Email)
	return nil
}

// Before serialization: masking (Q and B both support InTransform; R supports OutTransform)
func (u *MaskedUser) OutTransform(ctx context.Context) error {
	if at := strings.Index(u.Email, "@"); at > 1 {
		u.Email = u.Email[:1] + "***" + u.Email[at:]
	}
	return nil
}
```

Pointer receivers are recommended (so modifications are written back). Returning an error short-circuits the request (through the binding/validation failure error path).

### 1.10 Middleware and Context Injection

**Three layers of middleware**:

```go
s.Use(rateLimit, recover)                 // ① global: before all routes
{ Middlewares: []any{middleware.Auth} }   // ② group: declared in Group.Middlewares, inherited down the tree
r.Use(gin.Logger())                       // ③ or use the framework's native mechanism directly
```

Group middleware types must match the current framework (gin expects `gin.HandlerFunc` / `func(*gin.Context)`); a type mismatch **panics at mount time** and names the group—silently ignoring it would amount to an invisible middleware.

**Context injection** (decorator):

```go
s.SetContextDecorator(func(c *gin.Context, ctx context.Context) context.Context {
	// Runs for every request (including failed validation) → keep it lightweight; put heavy work in middleware
	if u := parseToken(c.GetHeader("Authorization")); u != nil {
		ctx = contract.WithUser(ctx, u) // read in handlers via contract.CurrentUser(ctx)
	}
	return contract.WithFramework(ctx, c) // keep the framework handle
})
```

⚠️ `SetContextDecorator` **wholesale-replaces** the default decorator (the default behavior injects `contract.WithFramework(ctx, c)`). If your custom decorator should still let `contract.Framework(ctx)` retrieve the framework context, chain it manually as shown above.

**Priority ladder** (when the template can't cover it, escalate in this order):

1. `header` tag binding for request headers
2. `contract.Response[R]`: per-call customization of status/headers/cookies
3. `contract.Framework(ctx)`: direct access to `*gin.Context` (couples the business layer to the framework; last resort only)

### 1.11 Other Frameworks Work the Same Way

Swapping frameworks with the same route tree only changes the assembly code. The echo version:

```go
import (
	serverecho "github.com/EdSan845D/oapi-hinge/serverecho"
	"github.com/labstack/echo/v4"
)

e := echo.New()
s := serverecho.New()
s.AddValidator(validator.Playground())
s.Mount(e.Group(routes.BasePath), routes.All()) // the same routes.All()
```

Only three things differ: the adapter package name, the middleware type in `Group.Middlewares` (`echo.MiddlewareFunc`), and the `echo.Context` in the decorator signature. Binding rules, error semantics, the response envelope, and documentation generation are all identical. For more frameworks (chi, fiber…), see [Section 3](#3-writing-a-new-framework-adapter).

---

## 2. OpenAPI Documentation: Build-Time Isolation

### 2.1 Design: Why Isolate

- The documentation generator carries the `//go:build openapi` tag across the whole package, so `go build` (release) **contains none** of the dev-time dependencies such as kin-openapi / yaml
- The runtime and the documentation generator consume **the same route tree** (`routes.All()`); types are the contract, so docs and implementation can't drift apart
- `build.sh -r` automatically verifies the release dependency chain (`go list -deps` checks for openapi3), preventing accidental imports

### 2.2 Three Steps to Wire It In

**① Create `main_doc.go`** (mutually exclusive with `main.go` via build tags):

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
	// Optional: middleware documentation hooks (see 2.4)
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

**② Generate**:

```bash
go run -tags openapi . -out openapi.yaml
# or, if the project ships build.sh: ./build.sh -s
```

**③ Commit/publish the spec**: `openapi.yaml` can be committed to the repository (for CI drift checks and the /docs page), or generated only in the release pipeline.

### 2.3 Documentation Options (Option)

| Option | Purpose |
|---|---|
| `OptionWithDocInfo(info)` | title / version / description |
| `OptionWithServer(servers)` | server list (e.g. `/api`) |
| `OptionWithSecurity(schemes)` | security scheme definitions (e.g. BearerAuth, annotated onto operations via hooks) |
| `OptionWithEnvelope(env)` | inject the actual runtime envelope instance: success/failure response schemas are derived from the envelope (see 2.5, recommended) |
| `OptionWithEnvelopeSchema(fn)` | hand-written envelope schema hook (only for envelope shapes that can't be derived, such as map-style envelopes) |

### 2.4 Middleware Documentation Hooks

A middleware's effect cannot be derived from types via reflection, so documentation is filled in through a "function name → hook" mapping (`contract.FuncName` derives names via reflection—no hand-written strings):

```go
openapi.RegisterMiddlewareDoc(middleware.Auth, func(op *openapi3.Operation) {
	op.Security = &openapi3.SecurityRequirements{{"BearerAuth": {}}}
	op.Responses.Set("401", &openapi3.ResponseRef{Value: openapi3.NewResponse().
		WithDescription("Unauthorized：token 缺失或无效")})
})
```

- Hooks may modify any field of the operation (security, parameters, response codes…)
- Middleware without a registered hook still runs normally; it just stays out of the docs
- Auth annotations such as 401 / security are **declared on demand by hooks** (the generator never hard-codes 401 onto public endpoints)
- Registering the same middleware name twice panics (accident protection)

### 2.5 Response Envelope: Runtime Derivation (No Manual Pairing)

The envelope schema is derived by default from **the actually active envelope instance**: at generation time the generator calls the envelope's `Success/Failure` and reflects out its shape—docs and runtime are structurally identical by construction.

```go
// Put the envelope instance in a shared business package; both builds reference the same copy (single source of truth):
// app/routes or a config package
var APIEnvelope response.Envelope = response.DefaultEnvelope{}

// main.go (release build)
s.SetEnvelope(routes.APIEnvelope)

// main_doc.go (doc build)
openapi.Generate(out, routes.All(), openapi.OptionWithEnvelope(routes.APIEnvelope))
```

Precedence: `RouteMeta.Envelope` (route level, derived directly) > `OptionWithEnvelope` (global instance) > `OptionWithEnvelopeSchema` (hand-written hook: only needed when a RawEnvelope's map shape can't be reflected into fixed keys) > default envelope derivation.

### 2.6 Route-Level Documentation-Only Enhancements (DescribeRoute)

Error responses, response headers, OperationID, and other **documentation-only** information never enters the route table: register it in main_doc.go (compiled only with `-tags openapi`); release binaries carry zero of it. The key is the handler function reference (package.function derived via reflection—same mechanism as middleware hooks).

```go
// main_doc.go
openapi.DescribeRoute(handlers.GetUser, openapi.RouteDoc{
	OperationID: "getUserById",
	Errors: []openapi.ErrorDecl{
		{Status: 404, Description: "用户不存在"}, // response body derived from the envelope + default code injected
	},
	ResponseHeaders: []openapi.HeaderDecl{
		{Name: "X-RateLimit-Remaining", Description: "剩余配额"},
	},
	// Hide: true,  // remove from docs (still served at runtime)
	// Hook: func(op *openapi3.Operation) { /* fallback rewrite, applied last */ },
})
```

Rules:

- Registering the same key twice panics; a registration that matches nothing in the route tree produces a generation warning (registrations never fail silently)
- Error response bodies are derived from the envelope by default (`ErrorDecl.Schema` can override wholesale)
- Application order: middleware hooks first, then `RouteDoc.Hook` last
- `Deprecated` lives in `RouteMeta` (the runtime + docs shared layer; docs generate `deprecated: true`)
- For non-template routes use `openapi.RegisterManualPath(path, item)` (panics on conflicts with template routes)

### 2.7 Documentation–Code Consistency Guarantees

The generator derives everything from types and tags—no hand-written schemas:

- Q's `query/path/header/form` tags → parameters (type, description, required, default all come from the fields)
- B of a non-interface type → `application/json` request body (componentized `$ref` deduplication, recursion guards against stack overflow)
- R: ordinary types → the data schema inside the envelope; `*FileStream` → binary; interface types (Empty/any) → arbitrary JSON
- `DefaultStatusCode` → success response code
- Parameter types with a registered `ParamBinder` → schema annotated as string (the HTTP form is a raw string); `RegisterParamBinderSchema` can declare a precise schema
- `validate`/`binding` constraint tags → schema constraints; `example` tags → example values; pointer fields → nullable
- Type-level schema overrides: `RegisterTypeSchema[T]` / `RegisterTypeSchemaFunc[T]` (component replacement, `$ref` structure unchanged; decimal and custom-Marshaler types no longer degrade to string)
- Same-named types across packages → the component name is automatically upgraded to `lastpkg_TypeName` (with a generation warning; references updated in sync)
- Duplicate routes (same path+method) panic at generation time, surfacing registration mistakes early

### 2.8 Generation Commands and CI Advice

```bash
go run -tags openapi . -out openapi.yaml        # YAML
go run -tags openapi . -out openapi.json        # JSON
./build.sh -s                                    # the spec command of a project's own build.sh
```

CI advice (the repository's own `.github/workflows/ci.yml` already covers the first two):

```bash
go test -tags openapi ./...                       # documentation generator tests (invisible to plain go test)
go run -tags openapi . -out openapi.yaml
git diff --exit-code openapi.yaml                 # doc drift check: fails if routes changed without regenerating
```

For spec conformance checks in CI, switch `openapi.Generate` to `openapi.GenerateStrict` (any warning becomes an error).

### 2.9 Serving Documentation Online

The spec is standard OpenAPI 3.1 and works with any renderer. The example serves a `/docs` page with scalar-go (reading `openapi.yaml`); in production, isolate documentation endpoints behind an intranet boundary or auth using a build tag or middleware, or `go:embed` the spec into the binary to avoid depending on working-directory files.

### 2.10 Comments as Documentation (Source Parsing)

**Source comments** on fields / structs / handlers can become documentation descriptions directly—comments exist only in source code, so release binaries carry zero of it. Enable with one line in main_doc.go:

```go
openapi.Generate(out, routes.All(), openapi.OptionWithSourceComments())
```

Extraction scope (only packages of the main module are parsed; third-party/stdlib are skipped):

| Comment location | Mapped to documentation |
|---|---|
| Comment above a field | description of query/header/path parameters and body fields |
| Comment above a struct | description of the components entry |
| Comment above a handler function | Summary/Description fallback (when RouteMeta doesn't specify them; first line → Summary, the rest → Description; with a comment present, the missing-summary warning is suppressed) |

```go
// User
type User struct {
	// User ID, globally unique
	ID string `json:"id"`
}

// Get user details; returns 404 if not found
func GetUser(ctx context.Context, req GetUserReq, _ any) (User, error) { ... }
```

**Precedence**: the `description` tag > comments (the built-in parser yields automatically when a description already exists).

**Custom parsing semantics**: to carry more structure in comments (e.g. example values), register a parser that takes over—it receives the raw comment text and a reference to the field's schema, and may rewrite anything:

```go
// main_doc.go: convention "// description | example:value"
openapi.RegisterCommentParser(func(src string, sch *openapi3.SchemaRef) *openapi3.SchemaRef {
	parts := strings.SplitN(src, "|", 2)
	desc := strings.TrimSpace(parts[0])
	example := ""
	if len(parts) == 2 {
		example = strings.TrimPrefix(strings.TrimSpace(parts[1]), "Example:")
	}
	return openapi.DescribeSchema(sch, desc, example) // $ref fields are wrapped in AllOf automatically
})
```

Rules: the parser is globally unique (double registration panics); a warning is generated when `OptionWithSourceComments` is not enabled; no DSL is baked into comments—the convention is yours to define.

---

## 3. Writing a New Framework Adapter

### 3.0 When You Need Your Own

- You want to integrate chi, fiber, enhanced net/http routers, or other new frameworks
- The existing gin/echo adapters can't satisfy your framework (rare: extension points already cover envelope/validation/error mapping/decoration/binding)
- The payoff: **the openapi documentation layer needs no changes at all** (it is framework-agnostic and consumes the contract.Group tree), and business code changes zero lines

### 3.1 What an Adapter Contains

Model it after `servergin` (the most complete) / `serverecho` (structure already aligned); a three-file layout is recommended:

| File | Responsibility | Size reference |
|---|---|---|
| `xxx.go` | `Server` struct + all extension-point Setters + the `Mount` entry | ~100 lines |
| `mount.go` | the `mountGroup`/`mount` request pipeline, error decisions (`resolveError`/`bindError`/`bindFail`), path-style conversion, `serveFile` | ~240 lines |
| `bind.go` | **only "extract raw values from the request"**: `bindQueryPath`/`bindFields`/`collectRawValues` | ~120 lines |

Binding metadata parsing (`contract.ParseFields`), scalar/slice/pointer/time binding (`contract.SetRaw/SetSliceValue`), and pipeline utilities (`contract.NewValue/CheckTarget/IsBodyMethod/CheckHandler/TransformIn/TransformOut`) are **all shared in the contract layer**—adapters don't copy any of this logic. This is the biggest difference from the early versions.

### 3.2 Step One: Create the Adapter Package

```bash
mkdir serverchi
```

Single-module repository: just create the package directory at the repository root—**no separate go.mod, no separate tag needed**—it is released under the main module's unified version.

- **Follow the existing package-name convention**: servergin is `package servergin`, serverecho is `package serverecho` (note: the `Package server` in their doc comments is a historical typo—the `package` line is authoritative); new adapters should use `package serverchi` to avoid import ambiguity
- Dependency discipline: an adapter package imports only contract + the target framework itself, never openapi

### 3.3 Step Two: Server Configuration and Extension Points

Copy the field set and Setters from `servergin/gin.go`, swapping gin types for the target framework's:

```go
type Server struct {
	middlewares []chi.HandlerFunc // the framework's middleware type
	validators  []validator.Func
	mapError    func(err error) (httpStatus, bizCode int)
	decorate    func(c *chi.Context, ctx context.Context) context.Context
	envelope    response.Envelope
	bindStatus  int // HTTP status for binding/validation failures (default 200)
}

func New() *Server {
	s := &Server{}
	s.mapError = defaultErrorMapper
	s.decorate = func(c *chi.Context, ctx context.Context) context.Context {
		return contract.WithFramework(ctx, c) // the framework injection point
	}
	s.envelope = response.DefaultEnvelope{}
	s.bindStatus = http.StatusOK
	return s
}
// Use / AddValidator / SetErrorMapper / SetContextDecorator /
// SetEnvelope / SetBindErrorStatus — word-for-word aligned with servergin
```

Copy `bindFail` as well: on non-200 the business code follows the status code, consistent with the `StatusError` convention.

### 3.4 Step Three: Mount / mountGroup

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
	sub := parent.Route(grp.Prefix, nil) // ← swap in the target framework's subgroup mechanism
	for _, mw := range grp.Middlewares {
		if fn, ok := mw.(chi.HandlerFunc); ok {
			sub.Use(fn)
			continue
		}
		// ❗A middleware type mismatch must panic—never silently ignore
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

### 3.5 Step Four: the mount Request Pipeline (the core)

This is the only part with real weight. Skeleton (for a complete runnable implementation, follow `servergin/mount.go` directly):

```go
func (s *Server) mount(g chi.Router, r contract.Route) {
	// ❗Signature validation at mount time: panic messages carry method+path—don't defer to reflection-call time
	if err := contract.CheckHandler(r.Handler); err != nil {
		panic(fmt.Sprintf("serverchi: mount %s %s: invalid handler: %v", r.Method, r.Path, err))
	}
	h := reflect.ValueOf(r.Handler)
	qType, bType := h.Type().In(1), h.Type().In(2)

	env := s.envelope // route-level Envelope > service-level
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
		// ❶ Decoration runs first: TransformIn/validators/TransformOut/handler share the same ctx
		ctx := s.decorate(c, c.Request.Context())

		// ❷ Q: interface placeholders (NoReq/any) skip binding and validation
		qArg := reflect.New(qType).Elem()
		if qType.Kind() != reflect.Interface {
			q := contract.NewValue(qType)
			if err := bindQueryPath(c, q.Interface()); err != nil {
				st, cd, msg := s.bindError(err) // binding errors also respect StatusError (a ParamBinder may return 404)
				fail(c, st, cd, msg)
				return
			}
			if err := contract.TransformIn(ctx, q.Interface()); err != nil { // ❗Q transforms too
				st, cd, msg := s.bindError(err)
				fail(c, st, cd, msg)
				return
			}
			qArg = q.Elem()
		}

		// ❸ B: IsBodyMethod + non-interface + body present; fixed JSON decoding
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

		// ❹ Validation
		if err := validator.Run(ctx, r.Method, contract.CheckTarget(qType, qArg), contract.CheckTarget(bType, bArg), s.validators...); err != nil {
			st, cd, msg := s.bindError(err)
			fail(c, st, cd, msg)
			return
		}

		// ❺ Call + error resolution (StatusError → StatusCoder → mapError)
		out := h.Call([]reflect.Value{reflect.ValueOf(ctx), qArg, bArg})
		if ei := out[1].Interface(); ei != nil {
			status, code, msg := resolveError(s, ei.(error))
			fail(c, status, code, msg)
			return
		}

		// ❻ Response[R] unwrap: Status / Headers / Cookies (write out via the framework's native API)
		status := successStatus
		respVal := out[0]
		if w, ok := respVal.Interface().(contract.ResponseWrapper); ok {
			if w.ResponseStatus() != 0 {
				status = w.ResponseStatus()
			}
			for k, v := range w.ResponseHeaders() {
				c.SetHeader(k, v) // ← framework API
			}
			for _, cookie := range w.ResponseCookies() {
				c.SetCookie(cookie)
			}
			respVal = reflect.ValueOf(w.ResponseData())
		}

		// ❼ Response transform
		tv, err := contract.TransformOut(ctx, respVal)
		if err != nil {
			status, code, msg := resolveError(s, err)
			fail(c, status, code, msg)
			return
		}
		respAny := tv.Interface()

		// ❽ FileStream: both pointer (nil→404) and value forms; everything else goes out through the envelope
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

`resolveError` / `resolveErrorStatus` / `bindError` / `bindFail` / `serveFile` are ported as-is from `servergin/mount.go` (only the write-out APIs change); in `serveFile`, note that **Size<=0 means chunked transfer** (don't hard-code Content-Length).

### 3.6 Step Five: bind.go Only Extracts Values

Framework differences are compressed into "how raw parameters are extracted":

```go
func bindQueryPath(c chi.Context, req any) error {
	rv := reflect.ValueOf(req)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return errors.New("invalid params type")
	}
	return bindFields(c, rv.Elem(), contract.ParseFields(rv.Elem().Type())) // shared metadata
}

func bindFields(c chi.Context, e reflect.Value, metas []contract.FieldMeta) error {
	for _, m := range metas {
		f := e.Field(m.Index)
		if len(m.Children) > 0 { /* embedded recursion, copy from servergin/bind.go (including the CanSet guard) */ }
		if binder, ok := contract.BinderFor(f.Type()); ok { // ❗ParamBinder hook comes first
			src := collectRawValues(c, m)
			if len(src) == 0 {
				continue // keep the zero value when the parameter is missing; required is left to the validator
			}
			v, err := binder(src)
			if err != nil {
				return err
			}
			f.Set(reflect.ValueOf(v))
			continue
		}
		if m.Path != "" { /* contract.SetRaw(f, framework path parameter, m.Path) */ }
		if m.Header != "" { /* contract.SetRaw(f, framework request header, m.Header) */ }
		if m.Query != "" || m.Form != "" {
			/* contract.SetSliceValue (multi-value) → contract.SetRaw → default tag fallback */
		}
	}
	return nil
}

// collectRawValues: collect the raw values of path/header/query into []string for the binder to consume
// (copy from servergin/bind.go, replacing the extraction APIs: c.Param / c.GetHeader / c.QueryArray)
```

Parse failures always surface as the `invalid %s` error returned by `contract.SetRaw`—**never swallow them silently**.

### 3.7 Step Six: Align Tests

Port servergin's tests to the new framework as the acceptance criteria for equivalence (the checklist is the spec):

- [ ] Health-check envelope output; path binding; `ErrNotFound` → 404
- [ ] `binding/validate required` validation; `Validate()` method; Playground integration
- [ ] `InTransform` (**both Q and B**) and `OutTransform`
- [ ] `StatusError` / `%w` wrap-through / custom `StatusCoder` / ordinary errors 200+code7
- [ ] `DefaultStatusCode` and dynamic `Response[R].Status` overrides
- [ ] Custom envelopes (service-level + route-level override); failure responses also go through the envelope
- [ ] query/default/header/slice (repeated + comma)/pointer/`time.Time` binding; errors on unsupported types
- [ ] `RegisterParamBinder` type takeover + StatusError joining the error chain
- [ ] FileStream's three forms (pointer / value / unknown Size)
- [ ] Group middleware inheritance; panic on middleware type mismatch; panic on invalid handler signatures
- [ ] Non-JSON Content-Type bodies are still decoded as JSON (same semantics as gin)
- [ ] `SetBindErrorStatus(400)` switching the binding-error status code

### 3.8 Checklist of Pitfalls

1. **Run `contract.CheckHandler` at mount time, always first**, with panic messages carrying method+path—reflection panics from signature errors are extremely cryptic
2. **Decode the body as fixed JSON**; don't use the framework's own `c.Bind()`—dispatching by Content-Type would make the same route table behave differently across adapters (echo's lesson)
3. **Don't miss Q's `TransformIn`**—historically both adapters only transformed B
4. **The two failure paths must not be mixed**: binding/validation failures → `bindError` (respects StatusError, otherwise `bindStatus`); handler business errors → `resolveError` (StatusError → StatusCoder → mapError)
5. **The error precedence order never changes**: `StatusError` → `StatusCoder` → `SetErrorMapper`; use `errors.As` to guarantee `%w` chain penetration
6. **Interface-placeholder checks**: when Q/B's `Kind() == reflect.Interface`, skip binding and validation (`NoReq`/`any`/`Empty` are all defined anys and must not fall into a type switch's interface case)
7. **A failed middleware assertion must panic** (with the group name and the actual type)—silently ignoring makes middleware invisible
8. **FileStream's dual forms** (pointer + value type); nil → 404; `Size<=0` outputs chunked
9. **Path-style conversion**: the route tree uniformly uses OpenAPI braces `{id}`; the adapter converts to the target syntax (gin/echo → `:id`, chi supports `{id}` natively, fiber → `:id`)
10. **Embedded-struct recursion before `IsExported`** (`contract.ParseFields` already handles this; watch out when writing your own traversal), and nil pointer embeds need a `CanSet` guard to prevent reflection panics
11. **The default tag**: when query/form is absent, fill it with `contract.SetRaw` (check the field's `IsZero()`), in sync with the docs' default
12. **Envelope override precedence**: `RouteMeta.Envelope` > `SetEnvelope`; **failure output also goes through the resolved env**
13. **The decorator runs first on every request** (including failed-validation requests)—the doc comment must state "keep it lightweight; put heavy work in middleware"
14. **The default decorator injects `contract.WithFramework`**—documentation semantics depend on it
15. **Don't reinvent shared caches**: `ParseFields`/`SetRaw`/`SetSliceValue`/`NewValue`/`CheckTarget`/`IsBodyMethod` all live in the contract layer; adapters only write the value-extraction functions
16. **Minimize dependencies**: an adapter package imports only contract + the framework itself; the documentation layer (kin-openapi) must never appear in an adapter's imports

---

## 4. FAQ

**Q: Want a pure RESTful style (no envelope, 4xx semantics)?**
Three lines: `s.SetEnvelope(response.RawEnvelope{})` + `s.SetErrorMapper(...)` + `s.SetBindErrorStatus(http.StatusBadRequest)`; pair it on the documentation side with `OptionWithEnvelopeSchema`.

**Q: Will the release binary carry documentation-generation dependencies?**
No. The `openapi` package is fully isolated behind `//go:build openapi`; `build.sh -r` automatically verifies the dependency chain (fails immediately if openapi3 is found).

**Q: How does a middleware's auth annotation get into the docs?**
`openapi.RegisterMiddlewareDoc(middleware.Auth, hook)`—register by function reference; declare 401/security inside the hook.

**Q: Want to bind `?ids=1,2` into a custom type / look up entities by ID?**
`contract.RegisterParamBinder` (see 1.4); returning `contract.StatusBadRequest/NotFound` as errors automatically joins the unified error chain.

**Q: Want `*gin.Context` inside a handler?**
Assert via `contract.Framework(ctx)` (the default decorator injects it); with a custom decorator, remember to chain `contract.WithFramework` manually. This is a last resort—prefer tags and `Response[R]`.

**Q: The scaffolded project doesn't compile?**
Confirm the oapi-hinge version in go.mod is ≥ v0.2.0 (single-module version) and re-run `go mod tidy`; if it still fails, please open an issue.

## 5. Testing

openapi requires the build tag:

```bash
# Default build (release semantics; excludes the openapi package)
go build ./... && go vet ./... && go test ./...

# Documentation generator (the tag is mandatory, or the tests won't run)
go test -tags openapi ./...

# Everything at once (build + vet + test)
./test.sh
```

Test file naming convention: `server_test.go` (basic pipeline), `feature_test.go` (capabilities), `parambinder_test.go` (binders), `bench_test.go` (benchmarks), `constraints_test.go` (constraint mapping), etc.—one file per domain.

CI (`.github/workflows/ci.yml`) triggers on pushes to main / alpha and automatically passes `-tags openapi`; `openapi.GenerateStrict` can be used for strict checks that turn warnings into failures.
