package servergin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/hinge"

	"github.com/gin-gonic/gin"
)

// gin 适配器测试（v0.2 内核）：与 serverecho/adapter_test.go、serverhttp_test.go
// 形态对称——路径参数 / 业务错误 / 绑定明细 / correlation / 框架注入 / FileStream。

type ginUserQ struct{ ID string }

func init() { gin.SetMode(gin.TestMode) }

func TestAdapterGetPathParamsEnvelope(t *testing.T) {
	k := NewKernel().SetEnvelope(hinge.DefaultEnvelope{})
	r := gin.New()

	bindQ := func(ctx context.Context, r hinge.RequestReader) (any, error) {
		id, ok := r.PathParam("id")
		if !ok || id == "" {
			return nil, &hinge.BindError{Fields: []hinge.BindFieldError{{Field: "id", In: "path", Msg: "必填"}}}
		}
		return &ginUserQ{ID: id}, nil
	}
	h := func(ctx context.Context, q, b any) (any, error) {
		return map[string]string{"id": q.(*ginUserQ).ID}, nil
	}
	ep := hinge.Endpoint{
		Owner: "UserEp", Handler: "GetUser",
		Method: http.MethodGet, Path: "/users/{id}",
		QType: hinge.Type[ginUserQ](), RType: hinge.Type[map[string]string](),
	}
	r.GET("/users/:id", Handle(k, ep, bindQ, nil, h))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/42", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code int               `json:"code"`
		Data map[string]string `json:"data"`
		Msg  string            `json:"msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rec.Body.String())
	}
	if body.Code != hinge.CodeOK || body.Data["id"] != "42" || body.Msg != "操作成功" {
		t.Fatalf("envelope mismatch: %+v", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}
}

// 默认壳 RawEnvelope：成功裸输出，失败 {"error": msg}（不加包装器）
func TestAdapterRawEnvelopeDefault(t *testing.T) {
	k := NewKernel()
	r := gin.New()

	h := func(ctx context.Context, q, b any) (any, error) {
		if q.(*ginUserQ).ID == "boom" {
			return nil, hinge.NotFound("用户不存在")
		}
		return map[string]string{"id": q.(*ginUserQ).ID}, nil
	}
	ep := hinge.Endpoint{
		Owner: "UserEp", Handler: "GetUser",
		Method: http.MethodGet, Path: "/users/{id}",
		QType: hinge.Type[ginUserQ](), RType: hinge.Type[map[string]string](),
	}
	r.GET("/users/:id", Handle(k, ep, func(ctx context.Context, r hinge.RequestReader) (any, error) {
		id, _ := r.PathParam("id")
		return &ginUserQ{ID: id}, nil
	}, nil, h))

	// 成功：裸数据，无 code/msg 包装
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/42", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"code"`) || strings.Contains(rec.Body.String(), `"msg"`) {
		t.Fatalf("默认裸壳不应有包装字段: body=%s", rec.Body.String())
	}
	var data map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil || data["id"] != "42" {
		t.Fatalf("raw body = %s", rec.Body.String())
	}

	// 失败：{"error": msg}
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/users/boom", nil))
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec2.Code, rec2.Body.String())
	}
	var fail struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &fail); err != nil || fail.Error != "用户不存在" {
		t.Fatalf("raw failure body = %s", rec2.Body.String())
	}
}

func TestAdapterBusinessErrorNotFound(t *testing.T) {
	k := NewKernel().SetEnvelope(hinge.DefaultEnvelope{})
	r := gin.New()

	h := func(ctx context.Context, q, b any) (any, error) {
		return nil, hinge.NotFound("用户不存在")
	}
	ep := hinge.Endpoint{Owner: "UserEp", Handler: "GetUser", Method: http.MethodGet, Path: "/users/{id}", RType: hinge.Type[hinge.Empty]()}
	r.GET("/users/:id", Handle(k, ep, nil, nil, h))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/999", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if body.Code != http.StatusNotFound || body.Msg != "用户不存在" {
		t.Fatalf("error envelope mismatch: %+v", body)
	}
}

func TestAdapterPostJSONBindErrors(t *testing.T) {
	k := NewKernel().SetEnvelope(hinge.DefaultEnvelope{})
	r := gin.New()

	type createBody struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	bindB := func(ctx context.Context, r hinge.RequestReader) (any, error) {
		raw, err := r.Body()
		if err != nil {
			return nil, err
		}
		var body createBody
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				return nil, err
			}
		}
		if body.Name == "" {
			be := &hinge.BindError{}
			be.AddField("name", "body", "必填字段缺失")
			return nil, be
		}
		return &body, nil
	}
	h := func(ctx context.Context, q, b any) (any, error) {
		body := b.(*createBody)
		return map[string]any{"name": body.Name, "age": body.Age}, nil
	}
	ep := hinge.Endpoint{
		Owner: "UserEp", Handler: "CreateUser",
		Method: http.MethodPost, Path: "/users",
		BType: hinge.Type[createBody](), RType: hinge.Type[map[string]any](),
	}
	r.POST("/users", Handle(k, ep, nil, bindB, h))

	// 缺 name：默认 bindStatus=200 → HTTP 200 + code=7 + bind_errors 明细
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"age":18}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（默认绑定失败状态码）; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code       int                    `json:"code"`
		BindErrors []hinge.BindFieldError `json:"bind_errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if body.Code != hinge.CodeError || len(body.BindErrors) != 1 || body.BindErrors[0].Field != "name" {
		t.Fatalf("bind errors mismatch: %+v", body)
	}
}

func TestAdapterCorrelation(t *testing.T) {
	k := NewKernel().SetCorrelation(true)
	r := gin.New()

	h := func(ctx context.Context, q, b any) (any, error) {
		return map[string]any{"cid": hinge.CorrelationIDFrom(ctx)}, nil
	}
	ep := hinge.Endpoint{Owner: "MiscEp", Handler: "Ping", Method: http.MethodGet, Path: "/ping", RType: hinge.Type[map[string]any]()}
	r.GET("/ping", Handle(k, ep, nil, nil, h))

	// 入站沿用
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(hinge.HeaderCorrelationID, "cid-123")
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get(hinge.HeaderCorrelationID); got != "cid-123" {
		t.Fatalf("X-Correlation-Id = %q, want cid-123; body=%s", got, rec.Body.String())
	}
	// 缺失生成 UUIDv4 形态
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/ping", nil))
	cid := rec2.Header().Get(hinge.HeaderCorrelationID)
	if len(cid) != 36 || strings.Count(cid, "-") != 4 {
		t.Fatalf("generated cid = %q", cid)
	}
}

// NewKernel 注入 *gin.Context 到 ctx（WithFramework 语义）
func TestAdapterFrameworkContextInjection(t *testing.T) {
	k := NewKernel()
	r := gin.New()

	var injected bool
	h := func(ctx context.Context, q, b any) (any, error) {
		_, injected = hinge.Framework(ctx).(*gin.Context)
		return map[string]any{"ok": injected}, nil
	}
	ep := hinge.Endpoint{Owner: "MiscEp", Handler: "Ping", Method: http.MethodGet, Path: "/ping", RType: hinge.Type[hinge.Empty]()}
	r.GET("/ping", Handle(k, ep, nil, nil, h))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))

	if !injected {
		t.Fatal("hinge.Framework(ctx) 未注入 gin.Context")
	}
}

// FileStream 直出（绕过壳）
func TestAdapterFileStream(t *testing.T) {
	k := NewKernel()
	r := gin.New()

	const content = "hello hinge stream gin"
	h := func(ctx context.Context, q, b any) (any, error) {
		return &hinge.FileStream{Name: "a.txt", Size: int64(len(content)), ContentType: "text/plain",
			Reader: strings.NewReader(content)}, nil
	}
	ep := hinge.Endpoint{Owner: "FileEp", Handler: "Download", Method: http.MethodGet, Path: "/file", RType: hinge.Type[hinge.Empty]()}
	r.GET("/file", Handle(k, ep, nil, nil, h))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/file", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != content {
		t.Fatalf("body = %q, want %q", rec.Body.String(), content)
	}
	// 壳 JSON 不应出现
	if strings.Contains(rec.Body.String(), `"code"`) {
		t.Fatalf("FileStream should bypass envelope: body=%s", rec.Body.String())
	}
}
