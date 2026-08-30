package servergin

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
	"github.com/gin-gonic/gin"
)

// ============ 新特性测试：RawBody / multipart 文件上传 ============

// callRaw 自定义 Content-Type + 原始 body 的测试请求
func callRaw(t *testing.T, e *gin.Engine, method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	return w
}

// ---- RawBody ----

func TestRawBody(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, b contract.RawBody) (string, error) {
		return "len=" + strconv.Itoa(len(b)) + ":" + string(b), nil
	}
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/raw",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, contract.RawBody, string]{Method: "POST", Path: "", Handler: h})},
	}})

	// JSON Content-Type 也不解码，整包字节透传（R=string 走统一壳，data 承载）
	w := callRaw(t, e, http.MethodPost, "/raw", strings.NewReader(`{"a":1}`), "application/json")
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

	// 空 body：合法（空字节）
	w = callRaw(t, e, http.MethodPost, "/raw", strings.NewReader(""), "")
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Code != 0 || body.Data != "len=0:" {
		t.Fatalf("raw empty = %d %q", body.Code, body.Data)
	}
}

// ---- multipart 文件上传 ----

type ginUploadReq struct {
	Title string                 `form:"title"`
	Files []*contract.FileHeader `form:"files"`
}

func ginUploadHandler(ctx context.Context, _ contract.NoReq, b ginUploadReq) (map[string]any, error) {
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

type mpFile struct{ name, content string }

func buildMultipart(t *testing.T, fields map[string]string, files []mpFile) (io.Reader, string) {
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

func TestMultipartUpload(t *testing.T) {
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/up",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, ginUploadReq, map[string]any]{Method: "POST", Path: "", Handler: ginUploadHandler})},
	}})

	body, ct := buildMultipart(t,
		map[string]string{"title": "hello title"},
		[]mpFile{{"a.txt", "AAA"}, {"b.txt", "BBB"}},
	)
	w := callRaw(t, e, http.MethodPost, "/up", body, ct)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d body = %s", w.Code, w.Body.String())
	}
	for _, want := range []string{`"title":"hello title"`, `"n":2`, `"first_name":"a.txt"`, `"first":"AAA"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("missing %s in %s", want, w.Body.String())
		}
	}
}

// FileHeader 字段缺 form 标签：挂载期 panic（文件会静默丢失）
func TestMultipartMountPanicsWithoutFormTag(t *testing.T) {
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
	_ = newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/up",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, badUp, map[string]any]{Method: "POST", Path: "", Handler: h})},
	}})
}
