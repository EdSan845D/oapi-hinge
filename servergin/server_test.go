package servergin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/contract/response"
	"github.com/EdSan845D/oapi-hinge/contract/validator"

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

// ============ 升级能力测试：Envelope / StatusError / DefaultStatusCode / Transform / validate 标签 ============

// ---- 响应壳 ----

func rawHandler(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	return map[string]string{"status": "ok"}, nil
}

func TestCustomEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New().SetEnvelope(response.RawEnvelope{}).Mount(r.Group(""), testRoutes())

	w := call(t, r, http.MethodGet, "/api/health", "")
	// RawEnvelope：成功裸输出，无 {code,data,msg} 壳
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"code"`) {
		t.Fatalf("raw success = %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("raw success data missing: %s", w.Body.String())
	}
	// RawEnvelope：错误输出 {"error": ...}
	w = call(t, r, http.MethodGet, "/api/users/nope", "")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"error":"not found"`) {
		t.Fatalf("raw failure = %d %s", w.Code, w.Body.String())
	}
}

func TestRouteLevelEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	groups := []*contract.Group{
		{
			Prefix: "/api",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "GET", Path: "/health", Summary: "健康检查",
					Handler: rawHandler,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "GET", Path: "/raw", Summary: "裸输出",
					Envelope: response.RawEnvelope{},
					Handler:  rawHandler,
				}),
			},
		},
	}
	New().Mount(r.Group(""), groups)

	// 服务级默认壳
	w := call(t, r, http.MethodGet, "/api/health", "")
	if !strings.Contains(w.Body.String(), `"code":0`) {
		t.Fatalf("default envelope = %s", w.Body.String())
	}
	// 路由级 RawEnvelope 覆盖
	w = call(t, r, http.MethodGet, "/api/raw", "")
	if strings.Contains(w.Body.String(), `"code"`) {
		t.Fatalf("route envelope not applied: %s", w.Body.String())
	}
}

// ---- 错误携带状态码 ----

func statusErrorHandler(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	return nil, contract.NotFound("用户不存在")
}

func wrappedErrorHandler(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	// 错误链穿透：fmt.Errorf("%w") 包装后 errors.As 仍可识别
	return nil, fmt.Errorf("db layer: %w", contract.Forbidden("无权限"))
}

func customStatusCoderHandler(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	// 自定义 error 类型：仅实现 StatusCoder 接口
	return nil, myStatusErr{}
}

type myStatusErr struct{}

func (myStatusErr) Error() string   { return "custom status error" }
func (myStatusErr) StatusCode() int { return http.StatusTeapot }

func TestStatusError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	s := New()
	groups := []*contract.Group{
		{
			Prefix: "/s",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "GET", Path: "/nf", Summary: "404",
					Handler: statusErrorHandler,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "GET", Path: "/wrap", Summary: "错误链穿透",
					Handler: wrappedErrorHandler,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "GET", Path: "/custom", Summary: "自定义 StatusCoder",
					Handler: customStatusCoderHandler,
				}),
			},
		},
	}
	s.Mount(r.Group(""), groups)

	// StatusError：404 + 壳内消息
	w := call(t, r, http.MethodGet, "/s/nf", "")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "用户不存在") {
		t.Fatalf("status error = %d %s", w.Code, w.Body.String())
	}
	// 业务码跟随状态码（code=404）
	if !strings.Contains(w.Body.String(), `"code":404`) {
		t.Fatalf("biz code not following status: %s", w.Body.String())
	}

	// fmt.Errorf %w 包装穿透
	w = call(t, r, http.MethodGet, "/s/wrap", "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "无权限") {
		t.Fatalf("wrapped status error = %d %s", w.Code, w.Body.String())
	}

	// 自定义 StatusCoder
	w = call(t, r, http.MethodGet, "/s/custom", "")
	if w.Code != http.StatusTeapot {
		t.Fatalf("custom status coder = %d, want 418", w.Code)
	}
}

// ---- 成功状态码：RouteMeta.DefaultStatusCode + Response[R] 优先级 ----

func createdHandler(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	return map[string]string{"id": "u1"}, nil
}

func TestDefaultStatusCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	groups := []*contract.Group{
		{
			Prefix: "/d",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "POST", Path: "/create", Summary: "创建",
					DefaultStatusCode: http.StatusCreated,
					Handler:           createdHandler,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, any, contract.Response[map[string]string]]{
					Method:            "POST",
					Path:              "/override",
					Summary:           "动态覆盖",
					DefaultStatusCode: http.StatusCreated,
					Handler: func(ctx context.Context, _ contract.NoReq, _ any) (contract.Response[map[string]string], error) {
						// 逃生舱 2 动态状态码优先于路由级默认
						return contract.Response[map[string]string]{Status: http.StatusAccepted, Data: map[string]string{"id": "u2"}}, nil
					},
				}),
			},
		},
	}
	New().Mount(r.Group(""), groups)

	w := call(t, r, http.MethodPost, "/d/create", "")
	if w.Code != http.StatusCreated {
		t.Fatalf("default status = %d, want 201", w.Code)
	}
	w = call(t, r, http.MethodPost, "/d/override", "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("override status = %d, want 202", w.Code)
	}
}

// ---- InTransform / OutTransform ----

type transformReq struct {
	Name  string `json:"name" binding:"required"`
	Email string `json:"email" binding:"required"`
}

// InTransform：绑定后、校验前执行 —— trim 后必填校验通过
func (r *transformReq) InTransform(ctx context.Context) error {
	r.Name = strings.TrimSpace(r.Name)
	r.Email = strings.ToLower(r.Email)
	return nil
}

type transformUser struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// OutTransform：序列化前脱敏
func (u *transformUser) OutTransform(ctx context.Context) error {
	u.Password = "******"
	return nil
}

func transformHandler(ctx context.Context, _ contract.NoReq, req transformReq) (transformUser, error) {
	return transformUser{Name: req.Name, Email: req.Email, Password: "secret-123"}, nil
}

func TestTransform(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	groups := []*contract.Group{
		{
			Prefix: "/t",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[contract.NoReq, transformReq, transformUser]{
					Method: "POST", Path: "/users", Summary: "转换",
					Handler: transformHandler,
				}),
			},
		},
	}
	New().Mount(r.Group(""), groups)

	// InTransform 生效：name 带空格也能过 required（trim 后非空）；email 被小写化
	w := call(t, r, http.MethodPost, "/t/users", `{"name":"  Alice  ","email":"ALICE@EXAMPLE.COM"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("transform = %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"name":"Alice"`) || !strings.Contains(w.Body.String(), `"email":"alice@example.com"`) {
		t.Fatalf("InTransform not applied: %s", w.Body.String())
	}
	// OutTransform 生效：密码脱敏
	if !strings.Contains(w.Body.String(), `"password":"******"`) || strings.Contains(w.Body.String(), "secret-123") {
		t.Fatalf("OutTransform not applied: %s", w.Body.String())
	}
}

// ---- validate 标签必填 + Playground 完整规则 ----

type validateTagReq struct {
	Name string `json:"name" validate:"required"`
}

type playgroundReq struct {
	Email string `json:"email" validate:"required,email"`
}

func tagHandler(ctx context.Context, _ contract.NoReq, req validateTagReq) (map[string]string, error) {
	return map[string]string{"name": req.Name}, nil
}

func playgroundHandler(ctx context.Context, _ contract.NoReq, req playgroundReq) (map[string]string, error) {
	return map[string]string{"email": req.Email}, nil
}

func TestValidateTagAndPlayground(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	s := New().AddValidator(validator.Playground())
	groups := []*contract.Group{
		{
			Prefix: "/v",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[contract.NoReq, validateTagReq, map[string]string]{
					Method: "POST", Path: "/tag", Summary: "validate 标签必填",
					Handler: tagHandler,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, playgroundReq, map[string]string]{
					Method: "POST", Path: "/play", Summary: "playground 完整规则",
					Handler: playgroundHandler,
				}),
			},
		},
	}
	s.Mount(r.Group(""), groups)

	// validate:"required" 标签（不依赖 playground）
	w := call(t, r, http.MethodPost, "/v/tag", `{}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "required") {
		t.Fatalf("validate tag required = %d %s", w.Code, w.Body.String())
	}

	// playground：非法邮箱 400 语义（错误也是 200+code7，业务码约定）
	w = call(t, r, http.MethodPost, "/v/play", `{"email":"not-an-email"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Email") {
		t.Fatalf("playground email = %d %s", w.Code, w.Body.String())
	}
	// playground：合法邮箱通过
	w = call(t, r, http.MethodPost, "/v/play", `{"email":"ok@example.com"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ok@example.com") {
		t.Fatalf("playground ok = %d %s", w.Code, w.Body.String())
	}
}

// ---- 默认行为兼容：普通错误仍 200 + code:7 ----

func TestLegacyErrorBehavior(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New().Mount(r.Group(""), testRoutes())

	// 业务错误（无状态码）→ HTTP 200 + code:7，与原行为一致
	w := call(t, r, http.MethodPost, "/api/users", `{"name":"Cara","email":"not-an-email"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":7`) {
		t.Fatalf("legacy error = %d %s", w.Code, w.Body.String())
	}
	// 非 200 错误现在也走壳（原 {"error":...} → 统一壳，属刻意的格式一致性修复）
	w = call(t, r, http.MethodGet, "/api/users/nope", "")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"code":404`) {
		t.Fatalf("404 not enveloped = %d %s", w.Code, w.Body.String())
	}
}

// ---- StatusError 零值兜底 ----

func TestStatusErrorZeroValue(t *testing.T) {
	var se *contract.StatusError
	zero := &contract.StatusError{Err: errors.New("boom")}
	_ = se
	if zero.StatusCode() != http.StatusInternalServerError {
		t.Fatalf("zero status = %d, want 500", zero.StatusCode())
	}
}
