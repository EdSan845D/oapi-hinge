// Package servergin gin 适配器：把 hinge 内核挂到原生 Gin。
// v0.2 薄适配层：只负责「取值 + 写出」——请求管线（绑定/校验/调用/壳包装）
// 全部在 hinge 内核与生成代码中，本包不再有任何路由注册与业务装配逻辑。
package servergin

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"

	"github.com/EdSan845D/oapi-hinge/hinge"

	"github.com/gin-gonic/gin"
)

// NewKernel 创建注入 *gin.Context 的内核（WithFramework 语义）。
func NewKernel() *hinge.Kernel {
	k := hinge.NewKernel()
	k.SetContextDecorator(func(ctx context.Context, r hinge.RequestReader) context.Context {
		if rd, ok := r.(*Reader); ok {
			return hinge.WithFramework(ctx, rd.C)
		}
		return ctx
	})
	return k
}

// Handle 把内核端点适配为 gin.HandlerFunc。路由注册由生成代码完成：
//
//	r.GET("/users/:id", servergin.Handle(k, ep, bindQ, bindB, h, ics...))
//
// ics 为本端点的内核拦截器（oapi:interceptor 注解 / EntryPointConfig.Interceptors
// 的引用，声明序：EntryConfig → 结构体级 → 方法级），进 HandleWith 的 extra 内核链。
// 路径风格转换（{id} → :id）由 hinge gen 发射注册代码时完成。
func Handle(k *hinge.Kernel, ep hinge.Endpoint, bindQ, bindB hinge.Binder, h hinge.HandlerFunc, ics ...hinge.Interceptor) gin.HandlerFunc {
	inner := k.Handle(ep, bindQ, bindB, h, ics...)
	return func(c *gin.Context) {
		inner(&Reader{C: c}, &Sink{C: c})
	}
}

// Reader RequestReader 实现（gin）。
type Reader struct {
	C *gin.Context
}

func (r *Reader) Context() context.Context { return r.C.Request.Context() }

func (r *Reader) Method() string { return r.C.Request.Method }

func (r *Reader) PathParam(name string) (string, bool) {
	v := r.C.Param(name)
	return v, v != ""
}

func (r *Reader) QueryValues(name string) ([]string, bool) {
	vals := r.C.QueryArray(name)
	return vals, len(vals) > 0
}

func (r *Reader) FormValues(name string) ([]string, bool) {
	if err := r.C.Request.ParseForm(); err != nil {
		return nil, false
	}
	vals := r.C.Request.PostForm[name]
	return vals, len(vals) > 0
}

func (r *Reader) Header(name string) (string, bool) {
	v := r.C.GetHeader(name)
	return v, v != ""
}

func (r *Reader) Cookie(name string) (string, bool) {
	v, err := r.C.Cookie(name)
	if err != nil || v == "" {
		return "", false
	}
	return v, true
}

func (r *Reader) Body() ([]byte, error) {
	if r.C.Request.Body == nil {
		return nil, nil
	}
	return io.ReadAll(r.C.Request.Body)
}

func (r *Reader) MultipartForm() (*multipart.Form, error) {
	// 标准库对已解析请求为空操作；内存缓冲水位固定 32MB
	if err := r.C.Request.ParseMultipartForm(32 << 20); err != nil {
		return nil, err
	}
	return r.C.Request.MultipartForm, nil
}

// Sink Sink 实现（gin）。
type Sink struct {
	C *gin.Context
}

func (s *Sink) SetStatus(code int) { s.C.Status(code) }

func (s *Sink) SetHeader(k, v string) { s.C.Header(k, v) }

func (s *Sink) AddCookie(c *http.Cookie) { http.SetCookie(s.C.Writer, c) }

func (s *Sink) WriteJSON(status int, v any) {
	// PureJSON：不转义 HTML 字面量
	s.C.PureJSON(status, v)
}

func (s *Sink) WriteStream(f *hinge.FileStream) { writeStreamFile(s.C, f) }

// writeStreamFile 输出二进制流（ServeContent 条件请求 / DataFromReader / 分块回退）。
func writeStreamFile(c *gin.Context, f *hinge.FileStream) {
	if f.Reader == nil {
		// 无 Reader 的 FileStream 无法输出：防 panic（内核 serve 不校验 Reader）
		c.String(http.StatusInternalServerError, "invalid file stream: nil reader")
		return
	}
	contentType := f.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if f.CacheControl != "" {
		c.Header("Cache-Control", f.CacheControl)
	}
	if f.ETag != "" {
		c.Header("ETag", f.ETag)
	}
	disp := f.Disposition
	if disp == "" {
		disp = "attachment"
	}
	// 文件名缺省时不输出 Content-Disposition（避免空文件名的非法头）
	if f.Name != "" {
		c.Header("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disp, url.PathEscape(f.Name)))
	}
	// ServeContent 只在未显式设置时推断 Content-Type，先声明保证覆盖
	c.Header("Content-Type", contentType)
	if f.Size > 0 {
		if rs, ok := f.Reader.(io.ReadSeeker); ok {
			http.ServeContent(c.Writer, c.Request, "", f.ModTime, rs)
			return
		}
		c.DataFromReader(http.StatusOK, f.Size, contentType, f.Reader, nil)
		return
	}
	// Size 未知（<=0）：分块传输。DataFromReader 会写入 Content-Length，
	// 长度不符会截断/挂起响应，这里改用 io.Copy
	c.Status(http.StatusOK)
	if rc, ok := f.Reader.(io.ReadCloser); ok {
		defer rc.Close()
	}
	_, _ = io.Copy(c.Writer, f.Reader)
}
