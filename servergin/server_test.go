package servergin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"

	"github.com/gin-gonic/gin"
)

// 冒烟测试：统一注册表 -> Gin 适配器的 挂载/绑定/校验/响应壳/错误映射/二进制流 全链路。
// 使用自包含测试类型（不依赖 example 业务包）。

// ============ 本地测试业务 ============

type testUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type testUserReq struct {
	ID string `path:"id"`
}

type testCreateReq struct {
	Name  string `json:"name" binding:"required"`
	Email string `json:"email" binding:"required"`
}

func testHealth(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	return map[string]string{"status": "ok"}, nil
}

func testGetUser(ctx context.Context, req testUserReq, _ any) (testUser, error) {
	if req.ID != "u1" {
		return testUser{}, contract.ErrNotFound
	}
	return testUser{ID: "u1", Name: "Alice"}, nil
}

func testCreateUser(ctx context.Context, _ contract.NoReq, req testCreateReq) (testUser, error) {
	if !strings.Contains(req.Email, "@") {
		return testUser{}, errors.New("invalid email: must contain @")
	}
	return testUser{ID: "u9", Name: req.Name}, nil
}

// ============ 测试路由树 ============

func testRoutes() []*contract.Group {
	return []*contract.Group{
		{
			Prefix: "/api",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "GET", Path: "/health", Summary: "健康检查",
					Handler: testHealth,
				}),
				contract.New(contract.RouteMeta[testUserReq, any, testUser]{
					Method: "GET", Path: "/users/{id}", Summary: "用户详情",
					Handler: testGetUser,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, testCreateReq, testUser]{
					Method: "POST", Path: "/users", Summary: "创建用户",
					Handler: testCreateUser,
				}),
			},
		},
	}
}

func newTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New().Mount(r.Group(""), testRoutes())
	return r
}

func call(t *testing.T, e *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
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
	e := newTestEngine(t)

	// 健康检查：无参 + 统一响应壳
	w := call(t, e, http.MethodGet, "/api/health", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("health = %d %s", w.Code, w.Body.String())
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

	// 创建用户：JSON body + 自定义校验（非法邮箱）
	w = call(t, e, http.MethodPost, "/api/users", `{"name":"Cara","email":"not-an-email"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "invalid email") {
		t.Fatalf("create bad email = %d %s", w.Code, w.Body.String())
	}

	// 创建用户：binding required 校验
	w = call(t, e, http.MethodPost, "/api/users", `{"email":"cara@example.com"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "required") {
		t.Fatalf("create missing name = %d %s", w.Code, w.Body.String())
	}

	// 创建用户：成功
	w = call(t, e, http.MethodPost, "/api/users", `{"name":"Cara","email":"cara@example.com"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":0`) {
		t.Fatalf("create ok = %d %s", w.Code, w.Body.String())
	}
}

// TestGroupMiddleware 验证组中间件（gin.HandlerFunc）挂载与子组继承
func TestGroupMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	auth := func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
	groups := []*contract.Group{
		{
			Prefix:      "/secure",
			Middlewares: []any{auth},
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "GET", Path: "/ping", Summary: "受保护接口",
					Handler: testHealth,
				}),
			},
			Children: []*contract.Group{{
				Prefix: "/admin",
				Routes: []contract.Route{
					contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
						Method: "GET", Path: "/me", Summary: "子组继承中间件",
						Handler: testHealth,
					}),
				},
			}},
		},
	}
	New().Mount(r.Group(""), groups)

	w := call(t, r, http.MethodGet, "/secure/ping", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("parent no-token = %d, want 401", w.Code)
	}
	w = call(t, r, http.MethodGet, "/secure/admin/me", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("child no-token = %d, want 401 (inherited)", w.Code)
	}
}

// TestEscapeHatches 逃生舱：header 标签 / Response 定制 / Framework 注入
func TestEscapeHatches(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	s := New()

	type escapeHeaderReq struct {
		Lang string `header:"Accept-Language"`
	}
	escapeHeader := func(ctx context.Context, req escapeHeaderReq, _ any) (map[string]string, error) {
		return map[string]string{"lang": req.Lang}, nil
	}
	escapeCreated := func(ctx context.Context, _ contract.NoReq, _ any) (contract.Response[testUser], error) {
		return contract.Response[testUser]{
			Status:  http.StatusCreated,
			Headers: map[string]string{"X-Trace": "trace-1"},
			Cookies: []*http.Cookie{{Name: "sid", Value: "abc"}},
			Data:    testUser{ID: "u9", Name: "New"},
		}, nil
	}
	escapeFramework := func(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
		if g, ok := contract.Framework(ctx).(*gin.Context); ok {
			return map[string]string{"route": g.FullPath()}, nil
		}
		return map[string]string{"route": "unknown"}, nil
	}

	groups := []*contract.Group{
		{
			Prefix: "/escape",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[escapeHeaderReq, any, map[string]string]{
					Method: "GET", Path: "/header", Summary: "逃生舱1",
					Handler: escapeHeader,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, any, contract.Response[testUser]]{
					Method: "POST", Path: "/created", Summary: "逃生舱2",
					Handler: escapeCreated,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "GET", Path: "/framework", Summary: "逃生舱3",
					Handler: escapeFramework,
				}),
			},
		},
	}
	s.Mount(r.Group(""), groups)

	req := httptest.NewRequest(http.MethodGet, "/escape/header", nil)
	req.Header.Set("Accept-Language", "zh-CN")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), `"lang":"zh-CN"`) {
		t.Fatalf("header bind = %d %s", w.Code, w.Body.String())
	}

	w = call(t, r, http.MethodPost, "/escape/created", "")
	if w.Code != http.StatusCreated || w.Header().Get("X-Trace") != "trace-1" {
		t.Fatalf("created = %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), "sid=abc") {
		t.Fatalf("cookie missing")
	}

	w = call(t, r, http.MethodGet, "/escape/framework", "")
	if !strings.Contains(w.Body.String(), `"route":"/escape/framework"`) {
		t.Fatalf("framework = %d %s", w.Code, w.Body.String())
	}
}
