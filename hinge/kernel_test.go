package hinge

import (
	"context"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// ---- 测试 stub：RequestReader / Sink（手写绑定器与断言用） ----

type fakeReader struct {
	ctx        context.Context
	method     string
	pathParams map[string]string
	query      map[string][]string
	headers    map[string]string
	body       []byte
}

func (r *fakeReader) Context() context.Context {
	if r.ctx != nil {
		return r.ctx
	}
	return context.Background()
}

func (r *fakeReader) Method() string {
	if r.method == "" {
		return "GET"
	}
	return r.method
}

func (r *fakeReader) PathParam(n string) (string, bool) {
	v, ok := r.pathParams[n]
	return v, ok
}

func (r *fakeReader) QueryValues(n string) ([]string, bool) {
	v, ok := r.query[n]
	return v, ok && len(v) > 0
}

func (r *fakeReader) Header(n string) (string, bool) {
	v, ok := r.headers[n]
	return v, ok && v != ""
}

func (r *fakeReader) Cookie(string) (string, bool) { return "", false }

func (r *fakeReader) Body() ([]byte, error) { return r.body, nil }

func (r *fakeReader) MultipartForm() (*multipart.Form, error) { return nil, nil }

type fakeJSONOut struct {
	status int
	body   any
}

type fakeSink struct {
	status  int
	headers map[string]string
	cookies []*http.Cookie
	out     []fakeJSONOut
	stream  []*FileStream
}

func (s *fakeSink) SetStatus(code int) { s.status = code }

func (s *fakeSink) SetHeader(k, v string) {
	if s.headers == nil {
		s.headers = map[string]string{}
	}
	s.headers[k] = v
}

func (s *fakeSink) AddCookie(c *http.Cookie) { s.cookies = append(s.cookies, c) }

func (s *fakeSink) WriteJSON(status int, v any) {
	s.out = append(s.out, fakeJSONOut{status, v})
	s.status = status
}

func (s *fakeSink) WriteStream(f *FileStream) { s.stream = append(s.stream, f) }

func (s *fakeSink) last() fakeJSONOut {
	if len(s.out) == 0 {
		return fakeJSONOut{}
	}
	return s.out[len(s.out)-1]
}

func TestHandleWithInterceptorErrorGoesToErrorChain(t *testing.T) {
	k := NewKernel()
	ep := Endpoint{Owner: "T", Handler: "Ping", Method: "GET", Path: "/ping"}
	ic := Interceptor(func(ctx context.Context, ep Endpoint, r RequestReader, s Sink, next func(context.Context) error) error {
		return NotFound("拦截器拒绝")
	})
	h := k.HandleWith(ep, []Interceptor{ic}, nil, nil, func(ctx context.Context, q, b any) (any, error) { return "x", nil })

	sink := &fakeSink{}
	h(&fakeReader{}, sink)

	// RawEnvelope 默认壳：{"error": msg}
	last := sink.last()
	if last.status != 404 {
		t.Fatalf("status = %d, want 404", last.status)
	}
	m, ok := last.body.(map[string]any)
	if !ok || m["error"] != "拦截器拒绝" {
		t.Fatalf("error body = %v", last.body)
	}
}

// ---- 绑定失败：默认统一壳（bind_errors 明细）与裸壳（error 汇总） ----

func TestHandleWithBindFailDefaultEnvelope(t *testing.T) {
	k := NewKernel().SetEnvelope(DefaultEnvelope{})
	bindQ := func(ctx context.Context, r RequestReader) (any, error) {
		be := &BindError{}
		be.AddField("name", "body", "is required")
		return nil, be
	}
	ep := Endpoint{Owner: "T", Handler: "Create", Method: "POST", Path: "/users"}
	h := k.Handle(ep, bindQ, nil, func(ctx context.Context, q, b any) (any, error) { return "x", nil })

	sink := &fakeSink{}
	h(&fakeReader{body: []byte(`{}`)}, sink)

	last := sink.last()
	if last.status != http.StatusOK {
		t.Fatalf("默认 bindStatus=200，got %d", last.status)
	}
	reply, ok := last.body.(Reply[any])
	if !ok || reply.Code != CodeError || len(reply.BindErrors) != 1 || reply.BindErrors[0].Field != "name" {
		t.Fatalf("bind fail reply = %+v", last.body)
	}
}

func TestHandleWithBindFailRawEnvelope(t *testing.T) {
	k := NewKernel() // 默认裸壳
	bindQ := func(ctx context.Context, r RequestReader) (any, error) {
		be := &BindError{}
		be.AddField("name", "body", "is required")
		return nil, be
	}
	ep := Endpoint{Owner: "T", Handler: "Create", Method: "POST", Path: "/users"}
	h := k.Handle(ep, bindQ, nil, func(ctx context.Context, q, b any) (any, error) { return "x", nil })

	sink := &fakeSink{}
	h(&fakeReader{body: []byte(`{}`)}, sink)

	last := sink.last()
	if last.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", last.status)
	}
	m, ok := last.body.(map[string]any)
	if !ok || !strings.Contains(m["error"].(string), "name: is required") {
		t.Fatalf("raw bind fail body = %v", last.body)
	}
}

// ---- 关联 ID：入站沿用 / 缺失生成 / ctx 注入 ----

func TestHandleWithCorrelation(t *testing.T) {
	k := NewKernel().SetCorrelation(true)
	var seen string
	ep := Endpoint{Owner: "T", Handler: "Ping", Method: "GET", Path: "/ping"}
	h := k.Handle(ep, nil, nil, func(ctx context.Context, q, b any) (any, error) {
		seen = CorrelationIDFrom(ctx)
		return seen, nil
	})

	// 入站沿用
	sink := &fakeSink{}
	h(&fakeReader{headers: map[string]string{HeaderCorrelationID: "cid-123"}}, sink)
	if sink.headers[HeaderCorrelationID] != "cid-123" || seen != "cid-123" {
		t.Fatalf("inbound cid: header=%q ctx=%q", sink.headers[HeaderCorrelationID], seen)
	}

	// 缺失生成 UUIDv4 形态
	sink2 := &fakeSink{}
	h(&fakeReader{}, sink2)
	cid := sink2.headers[HeaderCorrelationID]
	if len(cid) != 36 || strings.Count(cid, "-") != 4 {
		t.Fatalf("generated cid = %q", cid)
	}
}

// ---- 映射：ErrNotFound → 404 贯通内核写出 ----

func TestHandleWithNotFoundMapping(t *testing.T) {
	k := NewKernel()
	ep := Endpoint{Owner: "T", Handler: "Get", Method: "GET", Path: "/users/{id}"}
	h := k.Handle(ep, nil, nil, func(ctx context.Context, q, b any) (any, error) {
		return nil, ErrNotFound
	})

	sink := &fakeSink{}
	h(&fakeReader{}, sink)

	last := sink.last()
	if last.status != 404 {
		t.Fatalf("status = %d, want 404", last.status)
	}
	m := last.body.(map[string]any)
	if m["error"] != "not found" {
		t.Fatalf("error body = %v", m)
	}
}

// ---- FileStream 直出（绕过壳）----

func TestHandleWithFileStreamBypassesEnvelope(t *testing.T) {
	k := NewKernel().SetEnvelope(DefaultEnvelope{}) // 即使统一壳，流也直出
	ep := Endpoint{Owner: "T", Handler: "File", Method: "GET", Path: "/file"}
	h := k.Handle(ep, nil, nil, func(ctx context.Context, q, b any) (any, error) {
		return &FileStream{Name: "a.txt"}, nil
	})

	sink := &fakeSink{}
	h(&fakeReader{}, sink)

	if len(sink.stream) != 1 || sink.stream[0].Name != "a.txt" {
		t.Fatalf("FileStream should bypass envelope: out=%v", sink.out)
	}
	if len(sink.out) != 0 {
		t.Fatalf("envelope JSON should not be written: %v", sink.out)
	}
}
