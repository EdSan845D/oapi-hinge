// Package serverhttp 标准库适配器：把 hinge 内核挂到 net/http
// （Go 1.22+ 方法 + 通配符路由模式，"GET /users/{id}"）。
// 可移植性试金石：只依赖 hinge 与标准库，证明内核真正框架无关——
// gin / echo 适配器（servergin / serverecho）与本包形态完全对称。
package serverhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"

	"github.com/EdSan845D/oapi-hinge/hinge"
)

// NewKernel 创建注入 *http.Request 的内核（WithFramework 语义）。
func NewKernel() *hinge.Kernel {
	k := hinge.NewKernel()
	k.SetContextDecorator(func(ctx context.Context, r hinge.RequestReader) context.Context {
		if rd, ok := r.(*Reader); ok {
			return hinge.WithFramework(ctx, rd.R)
		}
		return ctx
	})
	return k
}

// Handle 把内核端点适配为 http.HandlerFunc。路由注册由生成代码完成：
//
//	mux.HandleFunc("GET /users/{id}", serverhttp.Handle(k, ep, bindQ, bindB, h))
func Handle(k *hinge.Kernel, ep hinge.Endpoint, bindQ, bindB hinge.Binder, h hinge.HandlerFunc, mws ...any) http.HandlerFunc {
	inner := k.HandleWith(ep, AsInterceptors(ep, mws), bindQ, bindB, h)
	return func(w http.ResponseWriter, r *http.Request) {
		inner(&Reader{R: r}, &Sink{W: w, R: r})
	}
}

// Reader RequestReader 实现（标准库）。
type Reader struct {
	R *http.Request
}

func (r *Reader) Context() context.Context { return r.R.Context() }

func (r *Reader) Method() string { return r.R.Method }

func (r *Reader) PathParam(name string) (string, bool) {
	v := r.R.PathValue(name)
	return v, v != ""
}

func (r *Reader) QueryValues(name string) ([]string, bool) {
	vals := r.R.URL.Query()[name]
	return vals, len(vals) > 0
}

func (r *Reader) Header(name string) (string, bool) {
	v := r.R.Header.Get(name)
	return v, v != ""
}

func (r *Reader) Cookie(name string) (string, bool) {
	ck, err := r.R.Cookie(name)
	if err != nil || ck.Value == "" {
		return "", false
	}
	return ck.Value, true
}

func (r *Reader) Body() ([]byte, error) {
	if r.R.Body == nil {
		return nil, nil
	}
	return io.ReadAll(r.R.Body)
}

func (r *Reader) MultipartForm() (*multipart.Form, error) {
	if err := r.R.ParseMultipartForm(32 << 20); err != nil {
		return nil, err
	}
	return r.R.MultipartForm, nil
}

// Sink Sink 实现（标准库）。
type Sink struct {
	W http.ResponseWriter
	R *http.Request
}

func (s *Sink) SetStatus(code int) { s.W.WriteHeader(code) }

func (s *Sink) SetHeader(k, v string) { s.W.Header().Set(k, v) }

func (s *Sink) AddCookie(c *http.Cookie) { http.SetCookie(s.W, c) }

func (s *Sink) WriteJSON(status int, v any) {
	s.W.Header().Set("Content-Type", "application/json; charset=utf-8")
	s.W.WriteHeader(status)
	enc := json.NewEncoder(s.W)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func (s *Sink) WriteStream(f *hinge.FileStream) { serveFile(s.W, s.R, f) }

// serveFile 输出二进制流。Reader 实现 io.ReadSeeker 且 Size>0 时走
// http.ServeContent：自动支持 Range/206、If-None-Match / If-Modified-Since、
// If-Range 条件请求与 416；其余情况回退全量输出。
func serveFile(w http.ResponseWriter, r *http.Request, f *hinge.FileStream) {
	contentType := f.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if f.CacheControl != "" {
		w.Header().Set("Cache-Control", f.CacheControl)
	}
	if f.ETag != "" {
		w.Header().Set("ETag", f.ETag)
	}
	disp := f.Disposition
	if disp == "" {
		disp = "attachment"
	}
	// 文件名缺省时不输出 Content-Disposition（避免空文件名的非法头）
	if f.Name != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disp, url.PathEscape(f.Name)))
	}
	// ServeContent 只在未显式设置时推断 Content-Type，先声明保证覆盖
	w.Header().Set("Content-Type", contentType)
	if f.Size > 0 {
		if rs, ok := f.Reader.(io.ReadSeeker); ok {
			http.ServeContent(w, r, "", f.ModTime, rs)
			return
		}
		w.WriteHeader(http.StatusOK)
		if rc, ok := f.Reader.(io.ReadCloser); ok {
			defer rc.Close()
		}
		_, _ = io.Copy(w, f.Reader)
		return
	}
	// Size 未知（<=0）：分块传输。不要写 Content-Length（长度不符会截断/挂起响应）。
	w.WriteHeader(http.StatusOK)
	if rc, ok := f.Reader.(io.ReadCloser); ok {
		defer rc.Close()
	}
	_, _ = io.Copy(w, f.Reader)
}
