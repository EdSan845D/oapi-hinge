package servergin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/EdSan845D/oapi-hinge/contract"

	"github.com/gin-gonic/gin"
)

// ============ 补强测试：Q InTransform / default 标签 / 绑定错误状态码 /
// 绑定类型扩展（切片·指针·time.Time）/ FileStream 变体 / 挂载期校验 ============

// newTestEngineFor 带自定义配置的测试引擎（复用 server_test.go 的 call 等助手）
func newTestEngineFor(t *testing.T, config func(*Server), groups []*contract.Group) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	s := New()
	if config != nil {
		config(s)
	}
	s.Mount(r.Group(""), groups)
	return r
}

// ---- Q 的 InTransform：与 B 一致，绑定后、校验前自动调用 ----

type featQueryReq struct {
	Name string `query:"name"`
}

func (r *featQueryReq) InTransform(ctx context.Context) error {
	r.Name = "q:" + r.Name
	return nil
}

func featQueryHandler(ctx context.Context, req featQueryReq, _ any) (map[string]string, error) {
	return map[string]string{"name": req.Name}, nil
}

func TestQueryInTransform(t *testing.T) {
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/q",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[featQueryReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "Q 转换",
				Handler: featQueryHandler,
			}),
		},
	}})

	w := call(t, e, http.MethodGet, "/q/x?name=alice", "")
	if !strings.Contains(w.Body.String(), `"name":"q:alice"`) {
		t.Fatalf("Q InTransform not applied: %s", w.Body.String())
	}
}

// ---- default 标签：运行时缺省值（与文档 default 同步） ----

type featDefaultReq struct {
	Page int    `query:"page" default:"2"`
	Tag  string `query:"tag" default:"all"`
}

func featDefaultHandler(ctx context.Context, req featDefaultReq, _ any) (featDefaultReq, error) {
	return req, nil
}

func TestDefaultTagRuntime(t *testing.T) {
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/d",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[featDefaultReq, any, featDefaultReq]{
				Method: "GET", Path: "/list", Summary: "默认值",
				Handler: featDefaultHandler,
			}),
		},
	}})

	// 缺省 → 默认值生效
	w := call(t, e, http.MethodGet, "/d/list", "")
	if !strings.Contains(w.Body.String(), `"Page":2`) || !strings.Contains(w.Body.String(), `"Tag":"all"`) {
		t.Fatalf("defaults not applied: %s", w.Body.String())
	}
	// 显式传参覆盖默认值
	w = call(t, e, http.MethodGet, "/d/list?page=5&tag=hot", "")
	if !strings.Contains(w.Body.String(), `"Page":5`) || !strings.Contains(w.Body.String(), `"Tag":"hot"`) {
		t.Fatalf("explicit params should win: %s", w.Body.String())
	}
}

// ---- 绑定/校验失败的 HTTP 状态码：默认 200，可切换 400 ----

type featCreateReq struct {
	Name string `json:"name" binding:"required"`
}

func featCreateHandler(ctx context.Context, _ contract.NoReq, req featCreateReq) (featCreateReq, error) {
	return req, nil
}

var featCreateGroups = []*contract.Group{{
	Prefix: "/b",
	Routes: []contract.Route{
		contract.New(contract.RouteMeta[contract.NoReq, featCreateReq, featCreateReq]{
			Method: "POST", Path: "/create", Summary: "创建",
			Handler: featCreateHandler,
		}),
	},
}}

func TestBindErrorStatusDefault(t *testing.T) {
	e := newTestEngineFor(t, nil, featCreateGroups)

	// 存量行为：HTTP 200 + code=7
	w := call(t, e, http.MethodPost, "/b/create", `{}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":7`) {
		t.Fatalf("default bind status = %d %s", w.Code, w.Body.String())
	}
}

func TestBindErrorStatusCustom(t *testing.T) {
	e := newTestEngineFor(t, func(s *Server) { s.SetBindErrorStatus(http.StatusBadRequest) }, featCreateGroups)

	// 校验失败 → 400 + code 跟随状态码
	w := call(t, e, http.MethodPost, "/b/create", `{}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":400`) {
		t.Fatalf("custom bind status = %d %s", w.Code, w.Body.String())
	}
	// JSON 语法错误同样受控
	w = call(t, e, http.MethodPost, "/b/create", `{bad`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed json = %d", w.Code)
	}
	// 正常请求不受影响
	w = call(t, e, http.MethodPost, "/b/create", `{"name":"Cara"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":0`) {
		t.Fatalf("valid request broken: %d %s", w.Code, w.Body.String())
	}
}

// ---- 绑定类型扩展：重复/逗号分隔切片、指针、time.Time、不支持类型报错 ----

type featBindReq struct {
	IDs   []int     `query:"ids"`
	Tags  []string  `query:"tags"`
	When  time.Time `query:"when"`
	Count *int      `query:"count"`
}

func featBindHandler(ctx context.Context, req featBindReq, _ any) (featBindReq, error) {
	return req, nil
}

func TestBindExtendedTypes(t *testing.T) {
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/bind",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[featBindReq, any, featBindReq]{
				Method: "GET", Path: "/x", Summary: "扩展绑定",
				Handler: featBindHandler,
			}),
		},
	}})

	// 重复参数 + 指针 + time.Time（RFC3339）
	w := call(t, e, http.MethodGet, "/bind/x?ids=1&ids=2&tags=a&tags=b&when=2026-08-28T00:00:00Z&count=7", "")
	body := w.Body.String()
	for _, want := range []string{`"IDs":[1,2]`, `"Tags":["a","b"]`, `"When":"2026-08-28T00:00:00Z"`, `"Count":7`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}

	// 逗号分隔单值：?ids=3,4 等价 ?ids=3&ids=4
	w = call(t, e, http.MethodGet, "/bind/x?ids=3,4", "")
	if !strings.Contains(w.Body.String(), `"IDs":[3,4]`) {
		t.Fatalf("comma slice: %s", w.Body.String())
	}
}

type featBadBindReq struct {
	Bad chan int `query:"bad"`
}

func featBadHandler2(ctx context.Context, req featBadBindReq, _ any) (map[string]string, error) {
	return map[string]string{}, nil
}

func TestBindUnsupportedTypeError(t *testing.T) {
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/bad",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[featBadBindReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "不支持类型",
				Handler: featBadHandler2,
			}),
		},
	}})

	// 声明了绑定标签但不支持的类型：报错而非静默丢值
	w := call(t, e, http.MethodGet, "/bad/x?bad=x", "")
	if !strings.Contains(w.Body.String(), "unsupported field type") {
		t.Fatalf("unsupported type should error: %d %s", w.Code, w.Body.String())
	}
}

// ---- FileStream：指针/值类型/未知 Size ----

func TestFileStreamVariants(t *testing.T) {
	ptrHandler := func(ctx context.Context, _ contract.NoReq, _ any) (*contract.FileStream, error) {
		return &contract.FileStream{Name: "a.txt", ContentType: "text/plain", Size: 5, Reader: strings.NewReader("hello")}, nil
	}
	valHandler := func(ctx context.Context, _ contract.NoReq, _ any) (contract.FileStream, error) {
		return contract.FileStream{Name: "b.txt", ContentType: "text/plain", Size: 5, Reader: strings.NewReader("world")}, nil
	}
	unknownHandler := func(ctx context.Context, _ contract.NoReq, _ any) (*contract.FileStream, error) {
		return &contract.FileStream{Name: "c.txt", ContentType: "text/plain", Reader: strings.NewReader("chunked")}, nil
	}
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/f",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, *contract.FileStream]{Method: "GET", Path: "/ptr", Summary: "指针流", Handler: ptrHandler}),
			contract.New(contract.RouteMeta[contract.NoReq, any, contract.FileStream]{Method: "GET", Path: "/val", Summary: "值流", Handler: valHandler}),
			contract.New(contract.RouteMeta[contract.NoReq, any, *contract.FileStream]{Method: "GET", Path: "/unknown", Summary: "未知长度流", Handler: unknownHandler}),
		},
	}})

	// 指针类型：流输出 + Content-Disposition
	w := call(t, e, http.MethodGet, "/f/ptr", "")
	if w.Code != http.StatusOK || w.Body.String() != "hello" {
		t.Fatalf("ptr stream = %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Disposition") == "" {
		t.Fatal("Content-Disposition missing")
	}

	// 值类型：同样按流输出（不再被 JSON 序列化）
	w = call(t, e, http.MethodGet, "/f/val", "")
	if w.Body.String() != "world" {
		t.Fatalf("value stream: %s", w.Body.String())
	}

	// Size 未知：分块传输完整输出（不被 Content-Length 截断）
	w = call(t, e, http.MethodGet, "/f/unknown", "")
	if w.Body.String() != "chunked" {
		t.Fatalf("unknown size stream: %s", w.Body.String())
	}
}

// ---- 挂载期校验：不支持的中间件类型 / 非法 Handler 签名 直接 panic ----

func featPingHandler(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	return map[string]string{}, nil
}

func TestUnsupportedMiddlewarePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unsupported middleware type")
		}
	}()
	_ = newTestEngineFor(t, nil, []*contract.Group{{
		Prefix:      "/m",
		Middlewares: []any{"not-a-middleware"},
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/x", Handler: featPingHandler,
			}),
		},
	}})
}

func TestInvalidHandlerSignaturePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for invalid handler signature")
		}
	}()
	badHandler := func(a, b int) (string, error) { return "", nil }
	_ = newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/h",
		Routes: []contract.Route{
			{Method: "GET", Path: "/bad", Handler: badHandler},
		},
	}})
}

// ============ 上下文装饰前置：TransformIn / 校验器 / TransformOut / handler 共享已装饰 ctx ============
// （自 server_update_test.go 并入；用户注入属业务层关注点，测试内自带实现）

type userKey struct{}

// WithUser 将当前用户注入上下文（由运行时适配器的 ContextDecorator 调用）
func WithUser(ctx context.Context, user any) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// ErrNoUser 当前用户未注入
var ErrNoUser = errors.New("current user not found")

// CurrentUser 从上下文取出当前用户
func CurrentUser(ctx context.Context) (any, error) {
	user := ctx.Value(userKey{})
	if user == nil {
		return nil, ErrNoUser
	}
	return user, nil
}

type ctxUser struct{ ID string }

type ctxAwareReq struct {
	Name string `json:"name" binding:"required"`
}

func (r *ctxAwareReq) InTransform(ctx context.Context) error {
	// 前移后 InTransform 能读到 decorate 注入的用户
	u, err := CurrentUser(ctx)
	if err != nil {
		return err
	}
	r.Name = r.Name + ":" + u.(ctxUser).ID
	return nil
}

type ctxAwareOut struct {
	Name string `json:"name"`
}

func (o *ctxAwareOut) OutTransform(ctx context.Context) error {
	u, err := CurrentUser(ctx)
	if err != nil {
		return err
	}
	o.Name = o.Name + ":" + u.(ctxUser).ID
	return nil
}

func ctxAwareHandler(ctx context.Context, _ contract.NoReq, req ctxAwareReq) (ctxAwareOut, error) {
	return ctxAwareOut{Name: req.Name}, nil
}

func TestDecoratedCtxReachesAllPoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	s := New()
	s.SetContextDecorator(func(c *gin.Context, ctx context.Context) context.Context {
		return WithUser(ctx, ctxUser{ID: "u42"})
	})
	var validatorSawUser bool
	s.AddValidator(func(ctx context.Context, method string, q, b any) error {
		_, err := CurrentUser(ctx)
		validatorSawUser = err == nil
		return nil
	})
	groups := []*contract.Group{
		{
			Prefix: "/c",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[contract.NoReq, ctxAwareReq, ctxAwareOut]{
					Method: "POST", Path: "/users", Summary: "ctx aware",
					Handler: ctxAwareHandler,
				}),
			},
		},
	}
	s.Mount(r.Group(""), groups)

	w := call(t, r, http.MethodPost, "/c/users", `{"name":"Alice"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("ctx aware = %d %s", w.Code, w.Body.String())
	}
	// InTransform 追加 :u42 → handler 收到 Alice:u42 → OutTransform 再追加 :u42
	if !strings.Contains(w.Body.String(), `"name":"Alice:u42:u42"`) {
		t.Fatalf("decorated ctx not visible in transforms: %s", w.Body.String())
	}
	if !validatorSawUser {
		t.Fatal("custom validator cannot see decorated ctx")
	}
}
