// echo 适配器（v0.2 薄适配层）：把 hinge 内核挂到原生 Echo。
// 只负责「取值 + 写出」——请求管线（绑定/校验/调用/壳包装）全部在 hinge
// 内核与生成代码中，本文件不含任何路由注册与业务装配逻辑。
//
// 形态与 servergin / serverhttp 适配器对称；旧版 Server 装配器
//（echo.go / mount.go / bind.go）保留在原地，待主线分支统一清理。
package serverecho

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"

	"github.com/EdSan845D/oapi-hinge/hinge"

	"github.com/labstack/echo/v4"
)

// NewKernel 创建注入 echo.Context 的内核（WithFramework 语义）。
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

// Handle 把内核端点适配为 echo.HandlerFunc。路由注册由生成代码完成：
//
//	e.GET("/users/:id", serverecho.Handle(k, ep, bindQ, bindB, h))
//
// 路径风格转换（{id} → :id）由 hinge gen 发射注册代码时完成。
func Handle(k *hinge.Kernel, ep hinge.Endpoint, bindQ, bindB hinge.Binder, h hinge.HandlerFunc) echo.HandlerFunc {
	inner := k.Handle(ep, bindQ, bindB, h)
	return func(c echo.Context) error {
		inner(&Reader{C: c}, &Sink{C: c})
		return nil
	}
}

// Reader RequestReader 实现（echo）。
type Reader struct {
	C echo.Context
}

func (r *Reader) Context() context.Context { return r.C.Request().Context() }

func (r *Reader) Method() string { return r.C.Request().Method }

func (r *Reader) PathParam(name string) (string, bool) {
	v := r.C.Param(name)
	return v, v != ""
}

func (r *Reader) QueryValues(name string) ([]string, bool) {
	vals := r.C.QueryParams()[name]
	return vals, len(vals) > 0
}

func (r *Reader) Header(name string) (string, bool) {
	v := r.C.Request().Header.Get(name)
	return v, v != ""
}

func (r *Reader) Cookie(name string) (string, bool) {
	ck, err := r.C.Cookie(name)
	if err != nil || ck.Value == "" {
		return "", false
	}
	return ck.Value, true
}

func (r *Reader) Body() ([]byte, error) {
	if r.C.Request().Body == nil {
		return nil, nil
	}
	return io.ReadAll(r.C.Request().Body)
}

func (r *Reader) MultipartForm() (*multipart.Form, error) {
	// 标准库对已解析请求为空操作；内存缓冲水位固定 32MB（echo 项目惯例一致）
	if err := r.C.Request().ParseMultipartForm(32 << 20); err != nil {
		return nil, err
	}
	return r.C.Request().MultipartForm, nil
}

// Sink Sink 实现（echo）。
type Sink struct {
	C echo.Context
}

func (s *Sink) SetStatus(code int) { s.C.Response().WriteHeader(code) }

func (s *Sink) SetHeader(k, v string) { s.C.Response().Header().Set(k, v) }

func (s *Sink) AddCookie(c *http.Cookie) { http.SetCookie(s.C.Response().Writer, c) }

func (s *Sink) WriteJSON(status int, v any) {
	// 不用 echo 的 c.JSON：其默认序列化转义 HTML 字面量，转义策略与其他适配器
	// 不一致；这里与 serverhttp 对齐：json.Encoder + SetEscapeHTML(false)。
	s.C.Response().Header().Set("Content-Type", "application/json; charset=utf-8")
	s.C.Response().WriteHeader(status)
	// echo.Response 自身实现 io.Writer（Write 透传底层 Writer 并跟踪 Size），直接作为编码目标
	enc := json.NewEncoder(s.C.Response())
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func (s *Sink) WriteStream(f *hinge.FileStream) { writeStreamFile(s.C, f) }

// writeStreamFile 输出二进制流（自 v0.1 serverecho/mount.go 的 serveFile 平移：
// Reader 可 Seek 且 Size>0 时走 http.ServeContent——自动支持 Range/206 多段、
// If-None-Match / If-Modified-Since / If-Range 条件请求与 416；其余情况回退
// 全量/分块输出）。
// 注：与旧版 mount.go 的 serveFile 并存（旧文件待分支上 git rm），故此处另取其名。
func writeStreamFile(c echo.Context, f *hinge.FileStream) {
	contentType := f.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if f.CacheControl != "" {
		c.Response().Header().Set("Cache-Control", f.CacheControl)
	}
	if f.ETag != "" {
		c.Response().Header().Set("ETag", f.ETag)
	}
	disp := f.Disposition
	if disp == "" {
		disp = "attachment"
	}
	// 文件名缺省时不输出 Content-Disposition（避免空文件名的非法头）
	if f.Name != "" {
		c.Response().Header().Set("Content-Disposition",
			fmt.Sprintf("%s; filename*=UTF-8''%s", disp, url.PathEscape(f.Name)))
	}
	// ServeContent 只在未显式设置时推断 Content-Type，先声明保证覆盖
	c.Response().Header().Set(echo.HeaderContentType, contentType)
	if f.Size > 0 {
		if rs, ok := f.Reader.(io.ReadSeeker); ok {
			http.ServeContent(c.Response(), c.Request(), "", f.ModTime, rs)
			return
		}
		// Size 已知时显式声明 Content-Length（echo 不会代写，缺失会退化为分块）
		c.Response().Header().Set(echo.HeaderContentLength, strconv.FormatInt(f.Size, 10))
	}
	// Size 未知（<=0）：分块传输。不要写 Content-Length（长度不符会截断/挂起响应）
	if rc, ok := f.Reader.(io.ReadCloser); ok {
		defer rc.Close()
	}
	_ = c.Stream(http.StatusOK, contentType, f.Reader)
}
