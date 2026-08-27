package serverecho

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/contract/response"

	"github.com/labstack/echo/v4"
)

// ============ 升级能力测试（关键路径，与 servergin 等价行为） ============

type echoUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type echoCreateReq struct {
	Name  string `json:"name" binding:"required"`
	Email string `json:"email" binding:"required"`
}

func echoCreate(ctx context.Context, _ contract.NoReq, req echoCreateReq) (echoUser, error) {
	return echoUser{ID: "u9", Name: req.Name}, nil
}

func echoNotFound(ctx context.Context, _ contract.NoReq, _ any) (echoUser, error) {
	return echoUser{}, contract.NotFound("用户不存在")
}

func echoHealth(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	return map[string]string{"status": "ok"}, nil
}

func echoRoutes() []*contract.Group {
	return []*contract.Group{
		{
			Prefix: "/api",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "GET", Path: "/health", Summary: "健康检查",
					Handler: echoHealth,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, echoCreateReq, echoUser]{
					Method: "POST", Path: "/users", Summary: "创建",
					DefaultStatusCode: http.StatusCreated,
					Handler:           echoCreate,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, any, echoUser]{
					Method: "GET", Path: "/nf", Summary: "404",
					Handler: echoNotFound,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "GET", Path: "/raw", Summary: "裸输出",
					Envelope: response.RawEnvelope{},
					Handler:  echoHealth,
				}),
			},
		},
	}
}

func testEchoServer() *echo.Echo {
	e := echo.New()
	New().Mount(e.Group(""), echoRoutes())
	return e
}

func TestEchoUpgrade(t *testing.T) {
	e := testEchoServer()

	// 成功 + DefaultStatusCode 201
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(`{"name":"Cara","email":"c@e.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("default status = %d, want 201", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":0`) {
		t.Fatalf("envelope missing: %s", rec.Body.String())
	}

	// 必填校验（binding 标签，validator 收拢后仍生效）
	req = httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(`{"email":"c@e.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "required") {
		t.Fatalf("required check lost: %s", rec.Body.String())
	}

	// StatusError → 404 + 壳内消息（非 200 也走壳）
	req = httptest.NewRequest(http.MethodGet, "/api/nf", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "用户不存在") {
		t.Fatalf("status error = %d %s", rec.Code, rec.Body.String())
	}

	// 路由级 RawEnvelope
	req = httptest.NewRequest(http.MethodGet, "/api/raw", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), `"code"`) {
		t.Fatalf("route envelope not applied: %s", rec.Body.String())
	}

	// 默认壳（服务级）
	req = httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"code":0`) {
		t.Fatalf("default envelope: %s", rec.Body.String())
	}
}
