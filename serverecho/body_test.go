package serverecho

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"

	"github.com/labstack/echo/v4"
)

// ============ 与 servergin 对齐的新特性测试：RawBody / multipart 文件上传 ============

// echoCallRaw 自定义 Content-Type + 原始 body 的测试请求
func echoCallRaw(t *testing.T, e *echo.Echo, method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// ---- RawBody ----

func TestEchoRawBody(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, b contract.RawBody) (string, error) {
		return "len=" + strconv.Itoa(len(b)) + ":" + string(b), nil
	}
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/raw",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, contract.RawBody, string]{Method: "POST", Path: "", Handler: h})},
	}})

	w := echoCallRaw(t, e, http.MethodPost, "/raw", strings.NewReader(`{"a":1}`), "application/json")
	var body struct {
		Code int    `json:"code"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Code != 0 || body.Data != `len=7:{"a":1}` {
		t.Fatalf("raw = %d %q", body.Code, body.Data)
	}

	w = echoCallRaw(t, e, http.MethodPost, "/raw", strings.NewReader(""), "")
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Code != 0 || body.Data != "len=0:" {
		t.Fatalf("raw empty = %d %q", body.Code, body.Data)
	}
}

// ---- multipart 文件上传 ----

type echoUploadReq struct {
	Title string                 `form:"title"`
	Files []*contract.FileHeader `form:"files"`
}

func echoUploadHandler(ctx context.Context, _ contract.NoReq, b echoUploadReq) (map[string]any, error) {
	f0, err := b.Files[0].Open()
	if err != nil {
		return nil, err
	}
	defer f0.Close()
	data, _ := io.ReadAll(f0)
	return map[string]any{
		"title": b.Title, "n": len(b.Files),
		"first_name": b.Files[0].Filename, "first": string(data),
	}, nil
}

type echoMpFile struct{ name, content string }

func echoBuildMultipart(t *testing.T, fields map[string]string, files []echoMpFile) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		fw, _ := mw.CreateFormField(k)
		_, _ = fw.Write([]byte(v))
	}
	for _, f := range files {
		fw, _ := mw.CreateFormFile("files", f.name)
		_, _ = fw.Write([]byte(f.content))
	}
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestEchoMultipartUpload(t *testing.T) {
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/up",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, echoUploadReq, map[string]any]{Method: "POST", Path: "", Handler: echoUploadHandler})},
	}})

	body, ct := echoBuildMultipart(t,
		map[string]string{"title": "hello title"},
		[]echoMpFile{{"a.txt", "AAA"}, {"b.txt", "BBB"}},
	)
	w := echoCallRaw(t, e, http.MethodPost, "/up", body, ct)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d body = %s", w.Code, w.Body.String())
	}
	for _, want := range []string{`"title":"hello title"`, `"n":2`, `"first_name":"a.txt"`, `"first":"AAA"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("missing %s in %s", want, w.Body.String())
		}
	}
}

// FileHeader 字段缺 form 标签：挂载期 panic
func TestEchoMultipartMountPanicsWithoutFormTag(t *testing.T) {
	type badUp struct {
		F *contract.FileHeader
	}
	h := func(ctx context.Context, _ contract.NoReq, b badUp) (map[string]any, error) {
		return nil, nil
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for FileHeader field without form tag")
		}
	}()
	_ = newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/up",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, badUp, map[string]any]{Method: "POST", Path: "", Handler: h})},
	}})
}
