package serverecho

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fuego-hinge/app/handlers"
	"fuego-hinge/app/routes"
	"fuego-hinge/internal/contract"

	"github.com/labstack/echo/v4"
)

// 冒烟测试：统一注册表 -> Echo 适配器的 挂载/绑定/校验/响应壳/错误映射/二进制流 全链路。
// 注意：routes.All() 中 users 组的中间件是 gin.HandlerFunc（middleware.Auth），
// echo 适配器按 echo.MiddlewareFunc 断言会跳过它——中间件与框架绑定，
// echo 项目应挂载 echo.MiddlewareFunc 版本的中间件（见 TestGroupMiddleware）。

func newTestEcho() *echo.Echo {
	e := echo.New()
	New().Mount(e.Group("/api"), routes.All())
	return e
}

func call(t *testing.T, e *echo.Echo, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	e.ServeHTTP(w, req)
	return w
}

func TestMountAndBinding(t *testing.T) {
	e := newTestEcho()

	// 健康检查：无参 + 统一响应壳
	w := call(t, e, http.MethodGet, "/api/health", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("health = %d %s", w.Code, w.Body.String())
	}

	// 用户列表：query 绑定 + 分页默认值
	w = call(t, e, http.MethodGet, "/api/users?page=1&size=1", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"total":2`) {
		t.Fatalf("users = %d %s", w.Code, w.Body.String())
	}

	// 用户详情：path 参数绑定
	w = call(t, e, http.MethodGet, "/api/users/u1", "")
	if !strings.Contains(w.Body.String(), "Alice") {
		t.Fatalf("user u1 = %d %s", w.Code, w.Body.String())
	}

	// 404：ErrNotFound 映射
	w = call(t, e, http.MethodGet, "/api/users/nope", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("user nope = %d, want 404", w.Code)
	}

	// 创建用户：JSON body + Validate() 自定义校验（非法邮箱）
	w = call(t, e, http.MethodPost, "/api/users", `{"name":"Cara","email":"not-an-email"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "invalid email") {
		t.Fatalf("create bad email = %d %s", w.Code, w.Body.String())
	}

	// 创建用户：标签必填校验
	w = call(t, e, http.MethodPost, "/api/users", `{"email":"cara@example.com"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "required") {
		t.Fatalf("create missing name = %d %s", w.Code, w.Body.String())
	}

	// 创建用户：成功
	w = call(t, e, http.MethodPost, "/api/users", `{"name":"Cara","email":"cara@example.com"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":0`) {
		t.Fatalf("create ok = %d %s", w.Code, w.Body.String())
	}

	// 删除用户：Empty 响应 data:null
	w = call(t, e, http.MethodDelete, "/api/users/u3", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"data":null`) {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}

	// 文件下载：二进制流
	w = call(t, e, http.MethodGet, "/api/files/sample.txt", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "fuego-hinge") {
		t.Fatalf("download = %d %s", w.Code, w.Body.String())
	}

	// 文件 404
	w = call(t, e, http.MethodGet, "/api/files/other.txt", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("download 404 = %d", w.Code)
	}
}

// TestGroupMiddleware 验证组中间件（echo.MiddlewareFunc）挂载与子组继承
func TestGroupMiddleware(t *testing.T) {
	e := echo.New()
	// echo 版鉴权中间件：拦截无 token 请求（显式声明为 echo.MiddlewareFunc，保证断言命中）
	auth := echo.MiddlewareFunc(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Request().Header.Get("Authorization") == "" {
				return c.JSON(http.StatusUnauthorized, echo.Map{"error": "unauthorized"})
			}
			return next(c)
		}
	})
	groups := []*contract.Group{
		{
			Prefix:      "/secure",
			Middlewares: []any{auth},
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[handlers.NoReq, any, map[string]string]{
					Method:  "GET",
					Path:    "/ping",
					Summary: "受保护接口",
					Handler: handlers.Health,
				}),
			},
			Children: []*contract.Group{{
				Prefix: "/admin",
				Routes: []contract.Route{
					contract.New(contract.RouteMeta[handlers.NoReq, any, map[string]string]{
						Method:  "GET",
						Path:    "/me",
						Summary: "子组继承中间件",
						Handler: handlers.Health,
					}),
				},
			}},
		},
	}
	New().Mount(e.Group(""), groups)

	// 无 token：父组与子组都应被拦截
	w := call(t, e, http.MethodGet, "/secure/ping", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("parent no-token = %d, want 401", w.Code)
	}
	w = call(t, e, http.MethodGet, "/secure/admin/me", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("child no-token = %d, want 401 (inherited)", w.Code)
	}

	// 带 token：放行
	req := httptest.NewRequest(http.MethodGet, "/secure/ping", nil)
	req.Header.Set("Authorization", "demo-token")
	rw := httptest.NewRecorder()
	e.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("parent with-token = %d, want 200", rw.Code)
	}
}

// TestCustomValidator 扩展点：AddValidator 自定义校验器
func TestCustomValidator(t *testing.T) {
	e := echo.New()
	s := New()
	s.AddValidator(func(ctx context.Context, method string, q, b any) error {
		if method == "POST" {
			if req, ok := b.(*handlers.CreateUserReq); ok && strings.Contains(req.Name, "blocked") {
				return errors.New("name is blocked by custom validator")
			}
		}
		return nil
	})
	s.Mount(e.Group("/api"), routes.All())

	w := call(t, e, http.MethodPost, "/api/users", `{"name":"blocked-user","email":"x@example.com"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "blocked") {
		t.Fatalf("custom validator = %d %s", w.Code, w.Body.String())
	}
}

// ============ 逃生舱测试：header 标签 / Response 定制 / Framework 注入 ============

type escapeHeaderReq struct {
	Lang string `header:"Accept-Language"`
	ID   string `path:"id"`
}

func escapeHeaderHandler(ctx context.Context, req escapeHeaderReq, _ any) (map[string]string, error) {
	return map[string]string{"lang": req.Lang, "id": req.ID}, nil
}

func escapeCreatedHandler(ctx context.Context, _ contract.NoReq, _ any) (contract.Response[handlers.User], error) {
	return contract.Response[handlers.User]{
		Status:  http.StatusCreated,
		Headers: map[string]string{"X-Trace": "trace-1"},
		Cookies: []*http.Cookie{{Name: "sid", Value: "abc"}},
		Data:    handlers.User{ID: "u9", Name: "New"},
	}, nil
}

func escapeFrameworkHandler(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	if ec, ok := contract.Framework(ctx).(echo.Context); ok {
		return map[string]string{"path": ec.Path()}, nil
	}
	return map[string]string{"path": "unknown"}, nil
}

func TestEscapeHatches(t *testing.T) {
	e := echo.New()
	s := New()
	groups := []*contract.Group{
		{
			Prefix: "/escape",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[escapeHeaderReq, any, map[string]string]{
					Method:  "GET",
					Path:    "/header/{id}",
					Summary: "逃生舱1：header 标签",
					Handler: escapeHeaderHandler,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, any, contract.Response[handlers.User]]{
					Method:  "POST",
					Path:    "/created",
					Summary: "逃生舱2：响应定制",
					Handler: escapeCreatedHandler,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method:  "GET",
					Path:    "/framework",
					Summary: "逃生舱3：框架注入",
					Handler: escapeFrameworkHandler,
				}),
			},
		},
	}
	s.Mount(e.Group("/api"), groups)

	// ① header 标签绑定
	req := httptest.NewRequest(http.MethodGet, "/api/escape/header/u1", nil)
	req.Header.Set("Accept-Language", "zh-CN")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"lang":"zh-CN"`) {
		t.Fatalf("header bind = %d %s", w.Code, w.Body.String())
	}

	// ② Response 定制：status 201 + header + cookie + envelope
	w = call(t, e, http.MethodPost, "/api/escape/created", "")
	if w.Code != http.StatusCreated {
		t.Fatalf("created status = %d, want 201", w.Code)
	}
	if w.Header().Get("X-Trace") != "trace-1" {
		t.Fatalf("custom header missing: %v", w.Header())
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), "sid=abc") {
		t.Fatalf("cookie missing: %v", w.Header().Get("Set-Cookie"))
	}
	if !strings.Contains(w.Body.String(), `"code":0`) || !strings.Contains(w.Body.String(), `"id":"u9"`) {
		t.Fatalf("created body = %s", w.Body.String())
	}

	// ③ Framework 注入断言
	w = call(t, e, http.MethodGet, "/api/escape/framework", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"path":"/api/escape/framework"`) {
		t.Fatalf("framework = %d %s", w.Code, w.Body.String())
	}
}