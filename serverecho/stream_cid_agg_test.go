package serverecho

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"

	"github.com/labstack/echo/v4"
)

// ============ 与 servergin 对齐的新特性测试：FileStream 语义 / Correlation ID / AggregateError ============

// echoCallHdr 带自定义请求头的测试请求
func echoCallHdr(t *testing.T, e *echo.Echo, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
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
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// ---- FileStream：Range / 条件请求 / 非 Seeker 回退 ----

func TestEchoFileStreamRange206(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, _ any) (*contract.FileStream, error) {
		return &contract.FileStream{Name: "a.bin", ContentType: "application/octet-stream", Size: 11, Reader: strings.NewReader("hello world")}, nil
	}
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/f",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, *contract.FileStream]{Method: "GET", Path: "", Handler: h})},
	}})

	w := echoCallHdr(t, e, http.MethodGet, "/f", "", map[string]string{"Range": "bytes=6-10"})
	if w.Code != http.StatusPartialContent || w.Body.String() != "world" {
		t.Fatalf("range = %d %q", w.Code, w.Body.String())
	}
	if cr := w.Header().Get("Content-Range"); !strings.Contains(cr, "6-10/11") {
		t.Fatalf("Content-Range = %q", cr)
	}
	if w.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatal("Accept-Ranges missing")
	}
}

func TestEchoFileStreamETag304(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, _ any) (*contract.FileStream, error) {
		return &contract.FileStream{ContentType: "text/plain", Size: 5, ETag: `"v1"`, Reader: strings.NewReader("hello")}, nil
	}
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/f",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, *contract.FileStream]{Method: "GET", Path: "", Handler: h})},
	}})

	w := echoCallHdr(t, e, http.MethodGet, "/f", "", map[string]string{"If-None-Match": `"v1"`})
	if w.Code != http.StatusNotModified {
		t.Fatalf("304 expected, got %d", w.Code)
	}
}

// 非 Seeker 的 Reader（Size 已知）：回退旧全量路径，行为不变
func TestEchoFileStreamNonSeekerFallback(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, _ any) (*contract.FileStream, error) {
		return &contract.FileStream{ContentType: "text/plain", Size: 5, Reader: struct{ io.Reader }{strings.NewReader("hello")}}, nil
	}
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/f",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, *contract.FileStream]{Method: "GET", Path: "", Handler: h})},
	}})

	w := echoCall(t, e, http.MethodGet, "/f", "", "")
	if w.Code != http.StatusOK || w.Body.String() != "hello" {
		t.Fatalf("fallback = %d %q", w.Code, w.Body.String())
	}
}

// ---- Correlation ID ----

func TestEchoCorrelation(t *testing.T) {
	var seen string
	h := func(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
		seen = contract.CorrelationIDFrom(ctx)
		return map[string]string{"ok": "1"}, nil
	}
	e := newEchoServerFor(t, func(s *Server) { s.SetCorrelation(true) }, []*contract.Group{{
		Prefix: "/c",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{Method: "GET", Path: "", Handler: h})},
	}})

	w := echoCallHdr(t, e, http.MethodGet, "/c", "", nil)
	cid := w.Header().Get(contract.HeaderCorrelationID)
	if cid == "" {
		t.Fatal("correlation header missing")
	}
	if seen == "" || seen != cid {
		t.Fatalf("ctx cid = %q, header = %q", seen, cid)
	}

	w = echoCallHdr(t, e, http.MethodGet, "/c", "", map[string]string{contract.HeaderCorrelationID: "abc-123"})
	if got := w.Header().Get(contract.HeaderCorrelationID); got != "abc-123" {
		t.Fatalf("inbound cid not reused: %q", got)
	}
	if seen != "abc-123" {
		t.Fatalf("ctx cid = %q, want abc-123", seen)
	}
}

// ---- AggregateError ----

func TestEchoAggregateError(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, _ any) ([]string, error) {
		return nil, &contract.AggregateError{
			StatusError: contract.StatusError{Status: http.StatusOK, Msg: "部分删除失败"},
			Total:       3,
			Failed:      []contract.ItemError{{Key: "a", Code: 7, Msg: "not found"}, {Key: "c", Code: 7, Msg: "locked"}},
		}
	}
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/b",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, []string]{Method: "GET", Path: "", Handler: h})},
	}})

	w := echoCall(t, e, http.MethodGet, "/b", "", "")
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

func TestEchoPlainErrorNoAggregateField(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, _ any) ([]string, error) {
		return nil, contract.NotFound("用户不存在")
	}
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/b",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, any, []string]{Method: "GET", Path: "", Handler: h})},
	}})
	w := echoCall(t, e, http.MethodGet, "/b", "", "")
	if strings.Contains(w.Body.String(), "aggregated_error") {
		t.Fatalf("plain error should not carry aggregated_error: %s", w.Body.String())
	}
}
