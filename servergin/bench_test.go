package servergin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/contract/response"

	"github.com/gin-gonic/gin"
)

// 性能基准：统一 Handler（反射适配）vs 原生 gin Handler。
// 运行：go test -bench . -benchmem -run '^$' ./internal/servergin/

// ============ 共享业务类型（两套引擎复用，保证业务逻辑一致） ============

type benchUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type benchListReq struct {
	Page int `query:"page" default:"1"`
	Size int `query:"size" default:"10"`
}

type benchCreateReq struct {
	Name string `json:"name" binding:"required"`
}

// 统一模板 Handler（servergin 反射调用）
func benchHealth(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	return map[string]string{"status": "ok"}, nil
}

func benchList(ctx context.Context, req benchListReq, _ any) ([]benchUser, error) {
	return []benchUser{{ID: "u1", Name: "Alice"}}, nil
}

func benchCreate(ctx context.Context, _ contract.NoReq, req benchCreateReq) (benchUser, error) {
	return benchUser{ID: "u9", Name: req.Name}, nil
}

// ============ servergin 引擎 ============

func newBenchServergin() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	s := New()
	groups := []*contract.Group{
		{
			Prefix: "/api",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "GET", Path: "/health", Summary: "健康检查",
					Handler: benchHealth,
				}),
				contract.New(contract.RouteMeta[benchListReq, any, []benchUser]{
					Method: "GET", Path: "/users", Summary: "用户列表",
					Handler: benchList,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, benchCreateReq, benchUser]{
					Method: "POST", Path: "/users", Summary: "创建用户",
					Handler: benchCreate,
				}),
			},
		},
	}
	s.Mount(r.Group(""), groups)
	return r
}

// ============ 原生 gin 引擎（等价业务逻辑，无反射） ============

func nativeHealth(c *gin.Context) {
	c.PureJSON(http.StatusOK, response.Response[any]{
		Code: response.CodeOK, Data: map[string]string{"status": "ok"}, Msg: "操作成功",
	})
}

func nativeList(c *gin.Context) {
	_ = c.Query("page")
	_ = c.Query("size")
	c.PureJSON(http.StatusOK, response.Response[any]{
		Code: response.CodeOK, Data: []benchUser{{ID: "u1", Name: "Alice"}}, Msg: "操作成功",
	})
}

func nativeCreate(c *gin.Context) {
	var req benchCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.PureJSON(http.StatusOK, response.Response[any]{Code: response.CodeError, Msg: err.Error()})
		return
	}
	if req.Name == "" {
		c.PureJSON(http.StatusOK, response.Response[any]{Code: response.CodeError, Msg: "name is required"})
		return
	}
	c.PureJSON(http.StatusOK, response.Response[any]{
		Code: response.CodeOK, Data: benchUser{ID: "u9", Name: req.Name}, Msg: "操作成功",
	})
}

func newBenchNative() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api")
	g.GET("/health", nativeHealth)
	g.GET("/users", nativeList)
	g.POST("/users", nativeCreate)
	return r
}

// ============ 基准执行 ============

func benchEngine(b *testing.B, e *gin.Engine, method, path, body string) {
	b.Helper()
	if body == "" {
		req := httptest.NewRequest(method, path, nil)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			w := httptest.NewRecorder()
			e.ServeHTTP(w, req)
		}
		return
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
	}
}

// --- 场景 1：无参 GET ---
func BenchmarkServergin_Health(b *testing.B) { benchEngine(b, newBenchServergin(), http.MethodGet, "/api/health", "") }
func BenchmarkNative_Health(b *testing.B)    { benchEngine(b, newBenchNative(), http.MethodGet, "/api/health", "") }

// --- 场景 2：query 绑定 GET ---
func BenchmarkServergin_List(b *testing.B) { benchEngine(b, newBenchServergin(), http.MethodGet, "/api/users?page=1&size=10", "") }
func BenchmarkNative_List(b *testing.B)    { benchEngine(b, newBenchNative(), http.MethodGet, "/api/users?page=1&size=10", "") }

// --- 场景 3：POST body + 校验 ---
func BenchmarkServergin_Create(b *testing.B) { benchEngine(b, newBenchServergin(), http.MethodPost, "/api/users", `{"name":"Alice"}`) }
func BenchmarkNative_Create(b *testing.B)    { benchEngine(b, newBenchNative(), http.MethodPost, "/api/users", `{"name":"Alice"}`) }