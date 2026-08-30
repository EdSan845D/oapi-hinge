package servergin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EdSan845D/oapi-hinge/contract"

	"github.com/gin-gonic/gin"
)

// ============ 新特性测试：FileStream 标准文件语义 / Correlation ID / AggregateError ============

// callHdr 带自定义请求头的测试请求
func callHdr(t *testing.T, e *gin.Engine, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	return w
}

// ---- FileStream：Range / 条件请求 / Disposition / CacheControl / 非 Seeker 回退 ----

func TestFileStreamRange206(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, _ any) (*contract.FileStream, error) {
		return &contract.FileStream{Name: "a.bin", ContentType: "application/octet-stream", Size: 11, Reader: strings.NewReader("hello world")}, nil
	}
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/f",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, *contract.FileStream]{Method: "GET", Path: "", Handler: h})},
	}})

	w := callHdr(t, e, http.MethodGet, "/f", "", map[string]string{"Range": "bytes=6-10"})
	if w.Code != http.StatusPartialContent || w.Body.String() != "world" {
		t.Fatalf("range = %d %q", w.Code, w.Body.String())
	}
	if cr := w.Header().Get("Content-Range"); !strings.Contains(cr, "6-10/11") {
		t.Fatalf("Content-Range = %q", cr)
	}
	if w.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatal("Accept-Ranges missing")
	}

	// 多段 Range 也可用（ServeContent 原生 multipart/byteranges）
	w = callHdr(t, e, http.MethodGet, "/f", "", map[string]string{"Range": "bytes=0-4,6-10"})
	if w.Code != http.StatusPartialContent {
		t.Fatalf("multi range = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "multipart/byteranges") {
		t.Fatalf("multi range Content-Type = %q", ct)
	}
}

func TestFileStreamETag304(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, _ any) (*contract.FileStream, error) {
		return &contract.FileStream{ContentType: "text/plain", Size: 5, ETag: `"v1"`, Reader: strings.NewReader("hello")}, nil
	}
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/f",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, *contract.FileStream]{Method: "GET", Path: "", Handler: h})},
	}})

	w := callHdr(t, e, http.MethodGet, "/f", "", map[string]string{"If-None-Match": `"v1"`})
	if w.Code != http.StatusNotModified {
		t.Fatalf("304 expected, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("304 body = %q", w.Body.String())
	}
}

func TestFileStreamHeadersAndDisposition(t *testing.T) {
	mod := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	h := func(ctx context.Context, _ contract.NoReq, _ any) (*contract.FileStream, error) {
		return &contract.FileStream{Name: "clip.mp4", ContentType: "video/mp4", Size: 9, Disposition: "inline", CacheControl: "private, max-age=3600", ModTime: mod, Reader: strings.NewReader("0123456789")}, nil
	}
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/f",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, *contract.FileStream]{Method: "GET", Path: "", Handler: h})},
	}})

	w := call(t, e, http.MethodGet, "/f", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline;") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "private, max-age=3600" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if lm := w.Header().Get("Last-Modified"); lm == "" {
		t.Fatal("Last-Modified missing")
	}
}

// 非 Seeker 的 Reader（Size 已知）：回退旧全量路径，行为不变
func TestFileStreamNonSeekerFallback(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, _ any) (*contract.FileStream, error) {
		return &contract.FileStream{ContentType: "text/plain", Size: 5, Reader: struct{ io.Reader }{strings.NewReader("hello")}}, nil
	}
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/f",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, *contract.FileStream]{Method: "GET", Path: "", Handler: h})},
	}})

	w := call(t, e, http.MethodGet, "/f", "")
	if w.Code != http.StatusOK || w.Body.String() != "hello" {
		t.Fatalf("fallback = %d %q", w.Code, w.Body.String())
	}
}

// ---- Correlation ID ----

func TestCorrelationEnabled(t *testing.T) {
	var seen string
	h := func(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
		seen = contract.CorrelationIDFrom(ctx)
		return map[string]string{"ok": "1"}, nil
	}
	e := newTestEngineFor(t, func(s *Server) { s.SetCorrelation(true) }, []*contract.Group{{
		Prefix: "/c",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{Method: "GET", Path: "", Handler: h})},
	}})

	// 无入站头：生成并回写，ctx 可读
	w := callHdr(t, e, http.MethodGet, "/c", "", nil)
	cid := w.Header().Get(contract.HeaderCorrelationID)
	if cid == "" {
		t.Fatal("correlation header missing")
	}
	if seen == "" || seen != cid {
		t.Fatalf("ctx cid = %q, header = %q", seen, cid)
	}

	// 入站头：原样沿用
	w = callHdr(t, e, http.MethodGet, "/c", "", map[string]string{contract.HeaderCorrelationID: "abc-123"})
	if got := w.Header().Get(contract.HeaderCorrelationID); got != "abc-123" {
		t.Fatalf("inbound cid not reused: %q", got)
	}
	if seen != "abc-123" {
		t.Fatalf("ctx cid = %q, want abc-123", seen)
	}
}

func TestCorrelationDisabledByDefault(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
		return map[string]string{}, nil
	}
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/c",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{Method: "GET", Path: "", Handler: h})},
	}})
	w := call(t, e, http.MethodGet, "/c", "")
	if w.Header().Get(contract.HeaderCorrelationID) != "" {
		t.Fatal("correlation should be off by default")
	}
}

// ---- AggregateError ----

func TestAggregateError(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, _ any) ([]string, error) {
		return nil, &contract.AggregateError{
			StatusError: contract.StatusError{Status: http.StatusOK, Msg: "部分删除失败"},
			Total:       3,
			Failed:      []contract.ItemError{{Key: "a", Code: 7, Msg: "not found"}, {Key: "c", Code: 7, Msg: "locked"}},
		}
	}
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/b",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, []string]{Method: "GET", Path: "", Handler: h})},
	}})

	w := call(t, e, http.MethodGet, "/b", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	var body struct {
		Code            int                  `json:"code"`
		Msg             string               `json:"msg"`
		AggregatedError []contract.ItemError `json:"aggregated_error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Code != 7 || body.Msg != "部分删除失败" {
		t.Fatalf("envelope = %d %q", body.Code, body.Msg)
	}
	if len(body.AggregatedError) != 2 || body.AggregatedError[0].Key != "a" {
		t.Fatalf("aggregated = %+v", body.AggregatedError)
	}
}

// 普通错误不携带 aggregated_error 键（响应体与旧版一致）
func TestPlainErrorNoAggregateField(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, _ any) ([]string, error) {
		return nil, contract.NotFound("用户不存在")
	}
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/b",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, []string]{Method: "GET", Path: "", Handler: h})},
	}})
	w := call(t, e, http.MethodGet, "/b", "")
	if strings.Contains(w.Body.String(), "aggregated_error") {
		t.Fatalf("plain error should not carry aggregated_error: %s", w.Body.String())
	}
}
