package serverecho

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EdSan845D/oapi-hinge/contract"

	"github.com/labstack/echo/v4"
)

// ============ 补强测试：与 servergin 对齐的核心行为 ============
// Q InTransform / default 标签 / 绑定错误状态码 / 严格 JSON /
// 绑定类型扩展 / FileStream 变体 / 挂载期校验

func echoCall(t *testing.T, e *echo.Echo, method, path, body, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func newEchoServerFor(t *testing.T, config func(*Server), groups []*contract.Group) *echo.Echo {
	t.Helper()
	e := echo.New()
	s := New()
	if config != nil {
		config(s)
	}
	s.Mount(e.Group(""), groups)
	return e
}

// ---- Q 的 InTransform ----

type featEchoQueryReq struct {
	Name string `query:"name"`
}

func (r *featEchoQueryReq) InTransform(ctx context.Context) error {
	r.Name = "q:" + r.Name
	return nil
}

func featEchoQueryHandler(ctx context.Context, req featEchoQueryReq, _ any) (map[string]string, error) {
	return map[string]string{"name": req.Name}, nil
}

func TestEchoQueryInTransform(t *testing.T) {
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/q",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[featEchoQueryReq, any, map[string]string]{
				Method: "GET", Path: "/x", Handler: featEchoQueryHandler,
			}),
		},
	}})

	w := echoCall(t, e, http.MethodGet, "/q/x?name=alice", "", "")
	if !strings.Contains(w.Body.String(), `"name":"q:alice"`) {
		t.Fatalf("Q InTransform not applied: %s", w.Body.String())
	}
}

// ---- default 标签 ----

type featEchoDefaultReq struct {
	Page int    `query:"page" default:"2"`
	Tag  string `query:"tag" default:"all"`
}

func featEchoDefaultHandler(ctx context.Context, req featEchoDefaultReq, _ any) (featEchoDefaultReq, error) {
	return req, nil
}

func TestEchoDefaultTagRuntime(t *testing.T) {
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/d",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[featEchoDefaultReq, any, featEchoDefaultReq]{
				Method: "GET", Path: "/list", Handler: featEchoDefaultHandler,
			}),
		},
	}})

	w := echoCall(t, e, http.MethodGet, "/d/list", "", "")
	if !strings.Contains(w.Body.String(), `"Page":2`) || !strings.Contains(w.Body.String(), `"Tag":"all"`) {
		t.Fatalf("defaults not applied: %s", w.Body.String())
	}
	w = echoCall(t, e, http.MethodGet, "/d/list?page=5&tag=hot", "", "")
	if !strings.Contains(w.Body.String(), `"Page":5`) || !strings.Contains(w.Body.String(), `"Tag":"hot"`) {
		t.Fatalf("explicit params should win: %s", w.Body.String())
	}
}

// ---- 绑定/校验失败的 HTTP 状态码 ----

type featEchoCreateReq struct {
	Name string `json:"name" binding:"required"`
}

func featEchoCreateHandler(ctx context.Context, _ contract.NoReq, req featEchoCreateReq) (featEchoCreateReq, error) {
	return req, nil
}

var featEchoCreateGroups = []*contract.Group{{
	Prefix: "/b",
	Routes: []contract.Route{
		contract.New(contract.RouteMeta[contract.NoReq, featEchoCreateReq, featEchoCreateReq]{
			Method: "POST", Path: "/create", Handler: featEchoCreateHandler,
		}),
	},
}}

func TestEchoBindErrorStatusDefault(t *testing.T) {
	e := newEchoServerFor(t, nil, featEchoCreateGroups)

	// 存量行为：HTTP 200 + code=7
	w := echoCall(t, e, http.MethodPost, "/b/create", `{}`, "application/json")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":7`) {
		t.Fatalf("default bind status = %d %s", w.Code, w.Body.String())
	}
}

func TestEchoBindErrorStatusCustom(t *testing.T) {
	e := newEchoServerFor(t, func(s *Server) { s.SetBindErrorStatus(http.StatusBadRequest) }, featEchoCreateGroups)

	w := echoCall(t, e, http.MethodPost, "/b/create", `{}`, "application/json")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":400`) {
		t.Fatalf("custom bind status = %d %s", w.Code, w.Body.String())
	}
	// JSON 语法错误同样受控
	w = echoCall(t, e, http.MethodPost, "/b/create", `{bad`, "application/json")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed json = %d", w.Code)
	}
	// 正常请求不受影响
	w = echoCall(t, e, http.MethodPost, "/b/create", `{"name":"Cara"}`, "application/json")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":0`) {
		t.Fatalf("valid request broken: %d %s", w.Code, w.Body.String())
	}
}

// ---- 严格 JSON：与 gin 对齐，不按 Content-Type 切换 form 绑定 ----

func TestEchoStrictJSONBinding(t *testing.T) {
	e := newEchoServerFor(t, nil, featEchoCreateGroups)

	// 非 JSON Content-Type 也按 JSON 解码（gin ShouldBindJSON 语义）
	w := echoCall(t, e, http.MethodPost, "/b/create", `{"name":"Cara"}`, "text/plain")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"Cara"`) {
		t.Fatalf("strict JSON bind = %d %s", w.Code, w.Body.String())
	}
	// 无 Content-Type 同样按 JSON 解码
	w = echoCall(t, e, http.MethodPost, "/b/create", `{"name":"Dora"}`, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"Dora"`) {
		t.Fatalf("no content-type bind = %d %s", w.Code, w.Body.String())
	}
}

// ---- 绑定类型扩展 ----

type featEchoBindReq struct {
	IDs   []int     `query:"ids"`
	Tags  []string  `query:"tags"`
	When  time.Time `query:"when"`
	Count *int      `query:"count"`
}

func featEchoBindHandler(ctx context.Context, req featEchoBindReq, _ any) (featEchoBindReq, error) {
	return req, nil
}

func TestEchoBindExtendedTypes(t *testing.T) {
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/bind",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[featEchoBindReq, any, featEchoBindReq]{
				Method: "GET", Path: "/x", Handler: featEchoBindHandler,
			}),
		},
	}})

	w := echoCall(t, e, http.MethodGet, "/bind/x?ids=1&ids=2&tags=a&tags=b&when=2026-08-28T00:00:00Z&count=7", "", "")
	body := w.Body.String()
	for _, want := range []string{`"IDs":[1,2]`, `"Tags":["a","b"]`, `"When":"2026-08-28T00:00:00Z"`, `"Count":7`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
	w = echoCall(t, e, http.MethodGet, "/bind/x?ids=3,4", "", "")
	if !strings.Contains(w.Body.String(), `"IDs":[3,4]`) {
		t.Fatalf("comma slice: %s", w.Body.String())
	}
}

type featEchoBadReq struct {
	Bad chan int `query:"bad"`
}

func featEchoBadHandler(ctx context.Context, req featEchoBadReq, _ any) (map[string]string, error) {
	return map[string]string{}, nil
}

func TestEchoBindUnsupportedTypeError(t *testing.T) {
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/bad",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[featEchoBadReq, any, map[string]string]{
				Method: "GET", Path: "/x", Handler: featEchoBadHandler,
			}),
		},
	}})

	w := echoCall(t, e, http.MethodGet, "/bad/x?bad=x", "", "")
	if !strings.Contains(w.Body.String(), "unsupported field type") {
		t.Fatalf("unsupported type should error: %d %s", w.Code, w.Body.String())
	}
}

// ---- FileStream：指针/值类型/未知 Size ----

func TestEchoFileStreamVariants(t *testing.T) {
	ptrHandler := func(ctx context.Context, _ contract.NoReq, _ any) (*contract.FileStream, error) {
		return &contract.FileStream{Name: "a.txt", ContentType: "text/plain", Size: 5, Reader: strings.NewReader("hello")}, nil
	}
	valHandler := func(ctx context.Context, _ contract.NoReq, _ any) (contract.FileStream, error) {
		return contract.FileStream{Name: "b.txt", ContentType: "text/plain", Size: 5, Reader: strings.NewReader("world")}, nil
	}
	unknownHandler := func(ctx context.Context, _ contract.NoReq, _ any) (*contract.FileStream, error) {
		return &contract.FileStream{Name: "c.txt", ContentType: "text/plain", Reader: strings.NewReader("chunked")}, nil
	}
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/f",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, *contract.FileStream]{Method: "GET", Path: "/ptr", Handler: ptrHandler}),
			contract.New(contract.RouteMeta[contract.NoReq, any, contract.FileStream]{Method: "GET", Path: "/val", Handler: valHandler}),
			contract.New(contract.RouteMeta[contract.NoReq, any, *contract.FileStream]{Method: "GET", Path: "/unknown", Handler: unknownHandler}),
		},
	}})

	w := echoCall(t, e, http.MethodGet, "/f/ptr", "", "")
	if w.Code != http.StatusOK || w.Body.String() != "hello" {
		t.Fatalf("ptr stream = %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Disposition") == "" {
		t.Fatal("Content-Disposition missing")
	}

	w = echoCall(t, e, http.MethodGet, "/f/val", "", "")
	if w.Body.String() != "world" {
		t.Fatalf("value stream: %s", w.Body.String())
	}

	w = echoCall(t, e, http.MethodGet, "/f/unknown", "", "")
	if w.Body.String() != "chunked" {
		t.Fatalf("unknown size stream: %s", w.Body.String())
	}
}

// ---- 挂载期校验 ----

func featEchoPingHandler(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	return map[string]string{}, nil
}

func TestEchoUnsupportedMiddlewarePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unsupported middleware type")
		}
	}()
	_ = newEchoServerFor(t, nil, []*contract.Group{{
		Prefix:      "/m",
		Middlewares: []any{"not-a-middleware"},
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/x", Handler: featEchoPingHandler,
			}),
		},
	}})
}

func TestEchoInvalidHandlerSignaturePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for invalid handler signature")
		}
	}()
	badHandler := func(a, b int) (string, error) { return "", nil }
	_ = newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/h",
		Routes: []contract.Route{
			{Method: "GET", Path: "/bad", Handler: badHandler},
		},
	}})
}
