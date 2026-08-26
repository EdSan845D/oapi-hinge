package servergin

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
	"fuego-hinge/internal/response"

	"github.com/gin-gonic/gin"
)

// 冒烟测试：统一注册表 -> Gin 适配器的 挂载/绑定/校验/响应壳/错误映射/二进制流 全链路。

func newTestEngine(t *testing.T) *gin.Engine {
	t.Setenv("FUEGO_HINGE_ENV", "dev")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New().Mount(r.Group(routes.BasePath), routes.All())
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

	// 创建用户：标签必填校验（gin validator 报错，code:7）
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

// 扩展点：AddValidator 自定义校验器
func TestCustomValidator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	t.Setenv("FUEGO_HINGE_ENV", "dev")
	s := New()
	s.AddValidator(func(ctx context.Context, method string, q, b any) error {
		if method == "POST" {
			if req, ok := b.(*handlers.CreateUserReq); ok && strings.Contains(req.Name, "blocked") {
				return errors.New("name is blocked by custom validator")
			}
		}
		return nil
	})
	s.Mount(r.Group(routes.BasePath), routes.All())

	w := call(t, r, http.MethodPost, "/api/users", `{"name":"blocked-user","email":"x@example.com"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "blocked") {
		t.Fatalf("custom validator = %d %s", w.Code, w.Body.String())
	}
}

// 扩展点：SetErrorMapper 自定义错误映射
func TestErrorMapper(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	t.Setenv("FUEGO_HINGE_ENV", "dev")
	s := New()
	s.SetErrorMapper(func(err error) (int, int) {
		if errors.Is(err, contract.ErrNotFound) {
			return http.StatusNotFound, 404
		}
		return http.StatusBadRequest, response.CodeError
	})
	s.Mount(r.Group(routes.BasePath), routes.All())

	// 业务错误（用户不存在但走非 404 路径：删除已删除的用户）-> 400
	w := call(t, r, http.MethodGet, "/api/users/nope", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("mapper 404 = %d", w.Code)
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
	if g, ok := contract.Framework(ctx).(*gin.Context); ok {
		return map[string]string{"route": g.FullPath()}, nil
	}
	return map[string]string{"route": "unknown"}, nil
}

func TestEscapeHatches(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
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
	s.Mount(r.Group("/api"), groups)

	// ① header 标签绑定
	req := httptest.NewRequest(http.MethodGet, "/api/escape/header/u1", nil)
	req.Header.Set("Accept-Language", "zh-CN")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"lang":"zh-CN"`) {
		t.Fatalf("header bind = %d %s", w.Code, w.Body.String())
	}

	// ② Response 定制：status 201 + header + cookie + envelope
	w = call(t, r, http.MethodPost, "/api/escape/created", "")
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
	w = call(t, r, http.MethodGet, "/api/escape/framework", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"route":"/api/escape/framework"`) {
		t.Fatalf("framework = %d %s", w.Code, w.Body.String())
	}
}