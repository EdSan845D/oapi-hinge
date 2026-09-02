package serverecho

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EdSan845D/oapi-hinge/hinge"

	"github.com/labstack/echo/v4"
)

// ---- 测试用入参类型（手写绑定器，不经生成器） ----

type adapterUserQ struct {
	ID string
}

type adapterCreateBody struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// ---- ① GET 路径参数 + 默认壳 {code, data, msg} ----

func TestAdapterGetPathParamsEnvelope(t *testing.T) {
	k := NewKernel()
	e := echo.New()

	bindQ := func(ctx context.Context, r hinge.RequestReader) (any, error) {
		id, ok := r.PathParam("id")
		if !ok || id == "" {
			return nil, &hinge.BindError{Fields: []hinge.BindFieldError{{Field: "id", In: "path", Msg: "必填"}}}
		}
		return &adapterUserQ{ID: id}, nil
	}
	h := func(ctx context.Context, q, b any) (any, error) {
		return map[string]string{"id": q.(*adapterUserQ).ID}, nil
	}
	ep := hinge.Endpoint{
		Owner:   "UserEp",
		Handler: "GetUser",
		Method:  http.MethodGet,
		Path:    "/users/{id}",
		QType:   hinge.Type[adapterUserQ](),
		RType:   hinge.Type[map[string]string](),
	}
	e.GET("/users/:id", Handle(k, ep, bindQ, nil, h))

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/42", nil))

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
	if body.Code != hinge.CodeOK {
		t.Fatalf("code = %d, want %d; body=%s", body.Code, hinge.CodeOK, rec.Body.String())
	}
	if body.Data["id"] != "42" {
		t.Fatalf("data.id = %q, want 42; body=%s", body.Data["id"], rec.Body.String())
	}
	if body.Msg != "操作成功" {
		t.Fatalf("msg = %q, want 默认壳成功文案; body=%s", body.Msg, rec.Body.String())
	}
	if ct := rec.Header().Get(echo.HeaderContentType); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
}

// ---- ② 业务错误 contract 语义：hinge.NotFound → HTTP 404 + code=404 ----

func TestAdapterBusinessErrorNotFound(t *testing.T) {
	k := NewKernel()
	e := echo.New()

	h := func(ctx context.Context, q, b any) (any, error) {
		return nil, hinge.NotFound("用户不存在")
	}
	ep := hinge.Endpoint{
		Owner:   "UserEp",
		Handler: "GetUser",
		Method:  http.MethodGet,
		Path:    "/users/{id}",
		RType:   hinge.Type[hinge.Empty](),
	}
	e.GET("/users/:id", Handle(k, ep, nil, nil, h))

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/999", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code int    `json:"code"`
		Data any    `json:"data"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rec.Body.String())
	}
	if body.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404; body=%s", body.Code, rec.Body.String())
	}
	if body.Msg != "用户不存在" {
		t.Fatalf("msg = %q, want 用户不存在; body=%s", body.Msg, rec.Body.String())
	}
}

// ---- ③ POST JSON body：空缺必填字段 → bind_errors 字段级明细 ----

func TestAdapterPostJSONBindErrors(t *testing.T) {
	k := NewKernel()
	e := echo.New()

	bindB := func(ctx context.Context, r hinge.RequestReader) (any, error) {
		raw, err := r.Body()
		if err != nil {
			return nil, err
		}
		var body adapterCreateBody
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				if be := hinge.AsBindError(err); be != nil {
					return nil, be
				}
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
		body := b.(*adapterCreateBody)
		return map[string]any{"name": body.Name, "age": body.Age}, nil
	}
	ep := hinge.Endpoint{
		Owner:   "UserEp",
		Handler: "CreateUser",
		Method:  http.MethodPost,
		Path:    "/users",
		BType:   hinge.Type[adapterCreateBody](),
		RType:   hinge.Type[map[string]any](),
	}
	e.POST("/users", Handle(k, ep, nil, bindB, h))

	// 缺 name：默认 bindStatus=200 → HTTP 200 + code=7 + bind_errors 明细
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"age":18}`))
	req.Header.Set(echo.HeaderContentType, "application/json")
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（默认绑定失败状态码）; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code       int                    `json:"code"`
		Msg        string                 `json:"msg"`
		BindErrors []hinge.BindFieldError `json:"bind_errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rec.Body.String())
	}
	if body.Code != hinge.CodeError {
		t.Fatalf("code = %d, want %d; body=%s", body.Code, hinge.CodeError, rec.Body.String())
	}
	if len(body.BindErrors) != 1 {
		t.Fatalf("bind_errors 数量 = %d, want 1; body=%s", len(body.BindErrors), rec.Body.String())
	}
	f := body.BindErrors[0]
	if f.Field != "name" || f.In != "body" || f.Msg == "" {
		t.Fatalf("bind_errors[0] = %+v, want {name body 非空 msg}", f)
	}

	// 完整 body：正常成功路径
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Ada","age":20}`))
	req2.Header.Set(echo.HeaderContentType, "application/json")
	e.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
	var okBody struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &okBody); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rec2.Body.String())
	}
	if okBody.Code != hinge.CodeOK || okBody.Data["name"] != "Ada" {
		t.Fatalf("unexpected success body: %s", rec2.Body.String())
	}
}

// ---- ④ correlation：SetCorrelation(true) → 回写 X-Correlation-Id ----

func TestAdapterCorrelation(t *testing.T) {
	k := NewKernel().SetCorrelation(true)
	e := echo.New()

	h := func(ctx context.Context, q, b any) (any, error) {
		return map[string]any{"cid": hinge.CorrelationIDFrom(ctx)}, nil
	}
	ep := hinge.Endpoint{
		Owner:   "MiscEp",
		Handler: "Ping",
		Method:  http.MethodGet,
		Path:    "/ping",
		RType:   hinge.Type[map[string]any](),
	}
	e.GET("/ping", Handle(k, ep, nil, nil, h))

	// 入站沿用
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(hinge.HeaderCorrelationID, "cid-123")
	e.ServeHTTP(rec, req)

	if got := rec.Header().Get(hinge.HeaderCorrelationID); got != "cid-123" {
		t.Fatalf("X-Correlation-Id = %q, want cid-123; body=%s", got, rec.Body.String())
	}
	var body struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rec.Body.String())
	}
	if body.Data["cid"] != "cid-123" {
		t.Fatalf("ctx 关联 ID = %v, want cid-123; body=%s", body.Data["cid"], rec.Body.String())
	}

	// 缺失生成 UUIDv4 形态
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/ping", nil))
	cid := rec2.Header().Get(hinge.HeaderCorrelationID)
	if cid == "" {
		t.Fatal("缺失入站头时应生成 X-Correlation-Id")
	}
	if len(cid) != 36 || strings.Count(cid, "-") != 4 {
		t.Fatalf("生成的关联 ID 非 UUIDv4 形态: %q", cid)
	}
}

// ---- 附加：NewKernel 注入 *echo.Context 到 ctx（WithFramework 语义） ----

func TestAdapterFrameworkContextInjection(t *testing.T) {
	k := NewKernel()
	e := echo.New()

	var injected bool
	h := func(ctx context.Context, q, b any) (any, error) {
		_, injected = hinge.Framework(ctx).(echo.Context)
		return map[string]any{"ok": injected}, nil
	}
	ep := hinge.Endpoint{
		Owner:   "MiscEp",
		Handler: "Ping",
		Method:  http.MethodGet,
		Path:    "/ping",
		RType:   hinge.Type[hinge.Empty](),
	}
	e.GET("/ping", Handle(k, ep, nil, nil, h))

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !injected {
		t.Fatal("hinge.Framework(ctx) 未注入 echo.Context")
	}
}

// ---- 附加：FileStream 输出（ServeContent 条件请求 + 分块回退） ----

func TestAdapterFileStream(t *testing.T) {
	k := NewKernel()
	e := echo.New()

	const content = "hello hinge stream"
	h := func(ctx context.Context, q, b any) (any, error) {
		return &hinge.FileStream{
			Name:        "sample.txt",
			Size:        int64(len(content)),
			ContentType: "text/plain",
			ModTime:     time.Now(),
			Reader:      strings.NewReader(content),
		}, nil
	}
	ep := hinge.Endpoint{
		Owner:   "FileEp",
		Handler: "Download",
		Method:  http.MethodGet,
		Path:    "/files/sample.txt",
		RType:   hinge.Type[hinge.Empty](),
	}
	e.GET("/files/sample.txt", Handle(k, ep, nil, nil, h))

	// 全量 200
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/files/sample.txt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != content {
		t.Fatalf("body = %q, want %q", rec.Body.String(), content)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "sample.txt") {
		t.Fatalf("Content-Disposition = %q", cd)
	}

	// Range → 206 条件请求（http.ServeContent 语义）
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/files/sample.txt", nil)
	req2.Header.Set("Range", "bytes=0-4")
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusPartialContent {
		t.Fatalf("Range status = %d, want 206", rec2.Code)
	}
	if rec2.Body.String() != content[:5] {
		t.Fatalf("Range body = %q, want %q", rec2.Body.String(), content[:5])
	}

	// 非 Seeker + 未知长度：分块回退全量输出
	k2 := NewKernel()
	e2 := echo.New()
	h2 := func(ctx context.Context, q, b any) (any, error) {
		return hinge.FileStream{
			ContentType: "text/plain",
			Reader:      io.NopCloser(strings.NewReader(content)), // NopCloser 不实现 Seek
		}, nil
	}
	e2.GET("/files/stream", Handle(k2, ep, nil, nil, h2))
	rec3 := httptest.NewRecorder()
	e2.ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/files/stream", nil))
	if rec3.Code != http.StatusOK {
		t.Fatalf("fallback status = %d, want 200", rec3.Code)
	}
	if rec3.Body.String() != content {
		t.Fatalf("fallback body = %q, want %q", rec3.Body.String(), content)
	}
}
