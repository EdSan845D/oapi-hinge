package server

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