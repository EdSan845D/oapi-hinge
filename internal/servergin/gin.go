// Package server 运行时适配器：把统一路由注册表(routes.All())挂载到原生 Gin。
// 职责：参数绑定 -> 校验 -> 上下文注入 -> Handler 调用 -> 统一响应序列化。
// 设计目标：业务层零框架依赖；解析/校验/错误映射/中间件均可扩展。
package servergin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"fuego-hinge/internal/contract"
	"fuego-hinge/internal/response"
	"fuego-hinge/internal/validator"

	"github.com/gin-gonic/gin"
)

// Server 运行时服务器装配器
type Server struct {
	middlewares []gin.HandlerFunc
	// 校验器：绑定后按注册顺序执行（内置标签必填校验 + Validate() 方法在绑定阶段完成）
	validators []validator.Func
	// 错误映射：默认 ErrNotFound -> 404，其余业务错误 -> 200 + code:7
	mapError func(err error) (httpStatus, bizCode int)
	// 上下文装饰：把 gin 上下文中的用户/claims 注入 handler 的 context.Context
	decorate func(c *gin.Context, ctx context.Context) context.Context
}

// New 创建 Server
func New() *Server {
	s := &Server{}
	s.mapError = defaultErrorMapper
	s.decorate = func(c *gin.Context, ctx context.Context) context.Context {
		return contract.WithFramework(ctx, c)
	}
	return s
}

// Use 扩展点：挂载全局中间件（鉴权/CORS/限流等），在业务路由之前执行。
// 按分组挂载的中间件请直接写在 contract.Group.Middlewares（随树继承）。
func (s *Server) Use(mw ...gin.HandlerFunc) *Server {
	s.middlewares = append(s.middlewares, mw...)
	return s
}

// AddValidator 扩展点：注册自定义校验器（绑定后执行），
// 见 validator 包：内置标签必填 + Validate() 接口调用
func (s *Server) AddValidator(v validator.Func) *Server {
	s.validators = append(s.validators, v)
	return s
}

// SetErrorMapper 扩展点：自定义 错误 -> (HTTP状态码, 业务code) 映射
func (s *Server) SetErrorMapper(fn func(err error) (httpStatus, bizCode int)) *Server {
	s.mapError = fn
	return s
}

// SetContextDecorator 扩展点：把 gin 上下文信息注入 handler 的 context.Context
func (s *Server) SetContextDecorator(fn func(c *gin.Context, ctx context.Context) context.Context) *Server {
	s.decorate = fn
	return s
}

// Mount 把路由分组树挂载到 gin.RouterGroup。
// 组中间件就地断言为 gin.HandlerFunc 后 Use（gin 自动向子组继承）。
func (s *Server) Mount(g *gin.RouterGroup, groups []*contract.Group) {
	api := g.Group("")
	if len(s.middlewares) > 0 {
		api.Use(s.middlewares...)
	}
	for _, grp := range groups {
		s.mountGroup(api, grp)
	}
}

func (s *Server) mountGroup(parent *gin.RouterGroup, grp *contract.Group) {
	sub := parent.Group(grp.Prefix)
	for _, mw := range grp.Middlewares {
		switch fn := mw.(type) {
		case gin.HandlerFunc:
			sub.Use(fn)
		case func(*gin.Context):
			sub.Use(gin.HandlerFunc(fn))
		}
	}
	for _, r := range grp.Routes {
		s.mount(sub, r)
	}
	for _, child := range grp.Children {
		s.mountGroup(sub, child)
	}
}

func (s *Server) mount(g *gin.RouterGroup, r contract.Route) {
	g.Handle(r.Method, ginPath(r.Path), func(c *gin.Context) {
		h := reflect.ValueOf(r.Handler)
		t := h.Type()

		// Q：query/path 参数解析
		q := newValue(t.In(1))
		if err := bindQueryPath(c, q.Interface()); err != nil {
			response.FailWithMessage(c, err.Error())
			return
		}

		// B：JSON body 解析（非 body 方法或无 body 路由跳过）
		b := newValue(t.In(2))
		if isBodyMethod(r.Method) && t.In(2).Kind() != reflect.Interface {
			if c.Request.Body != nil && c.Request.ContentLength > 0 {
				if err := c.ShouldBindJSON(b.Interface()); err != nil {
					response.FailWithMessage(c, err.Error())
					return
				}
			}
		}

		// 校验：内置（标签 required + Validate() 接口）+ 自定义校验器
		if err := validator.Run(c.Request.Context(), r.Method, q.Interface(), b.Interface(), s.validators...); err != nil {
			response.FailWithMessage(c, err.Error())
			return
		}

		// 上下文注入
		ctx := s.decorate(c, c.Request.Context())

		out := h.Call([]reflect.Value{reflect.ValueOf(ctx), q.Elem(), b.Elem()})
		if ei := out[1].Interface(); ei != nil {
			err := ei.(error)
			status, code := s.mapError(err)
			if status != http.StatusOK {
				c.AbortWithStatusJSON(status, gin.H{"error": err.Error()})
				return
			}
			response.FailWithCode(c, code, err.Error())
			return
		}

		resp := out[0].Interface()
		// 逃生舱 2：响应定制壳（Status/Headers/Cookies）
		status := http.StatusOK
		if w, ok := resp.(contract.ResponseWrapper); ok {
			if w.ResponseStatus() != 0 {
				status = w.ResponseStatus()
			}
			for k, v := range w.ResponseHeaders() {
				c.Header(k, v)
			}
			for _, cookie := range w.ResponseCookies() {
				http.SetCookie(c.Writer, cookie)
			}
			resp = w.ResponseData()
		}
		switch v := resp.(type) {
		case *contract.FileStream:
			if v == nil {
				response.FailWithMessage(c, "file not found")
				return
			}
			serveFile(c, v)
		case contract.Empty:
			response.OkWithDataStatus(c, status, nil)
		default:
			response.OkWithDataStatus(c, status, resp)
		}
	})
}

// 默认错误映射：ErrNotFound -> 404；其余业务错误 -> HTTP 200 + code:7
// （如需 RESTful 语义（如 400/422），用 SetErrorMapper 覆盖）
func defaultErrorMapper(err error) (int, int) {
	if errors.Is(err, contract.ErrNotFound) {
		return http.StatusNotFound, http.StatusNotFound
	}
	return http.StatusOK, response.CodeError
}

func newValue(t reflect.Type) reflect.Value {
	if t.Kind() == reflect.Pointer {
		return reflect.New(t.Elem())
	}
	return reflect.New(t)
}

func isBodyMethod(method string) bool {
	return method == "POST" || method == "PUT" || method == "PATCH"
}

// ginPath 把 OpenAPI 风格路径 /users/{id} 转为 Gin 风格 /users/:id
func ginPath(p string) string {
	return strings.NewReplacer("{", ":", "}", "").Replace(p)
}

// serveFile 输出二进制流（数据源为 io.Reader）
func serveFile(c *gin.Context, f *contract.FileStream) {
	contentType := f.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	extraHeaders := map[string]string{
		"Content-Disposition": fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(f.Name)),
	}
	c.DataFromReader(http.StatusOK, f.Size, contentType, f.Reader, extraHeaders)
}

// bindQueryPath 反射遍历 Q 结构体，绑定 query(优先)/form 与 path 标签字段（内嵌结构体递归展平）
func bindQueryPath(c *gin.Context, req any) error {
	rv := reflect.ValueOf(req)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return errors.New("invalid params type")
	}
	e := rv.Elem()
	for i := 0; i < e.NumField(); i++ {
		f, ft := e.Field(i), e.Type().Field(i)
		if !ft.IsExported() {
			continue
		}
		if ft.Anonymous {
			sub := f
			if sub.Kind() == reflect.Pointer {
				if sub.IsNil() {
					sub.Set(reflect.New(sub.Type().Elem()))
				}
				sub = sub.Elem()
			}
			if sub.Kind() == reflect.Struct {
				if err := bindQueryPath(c, sub.Addr().Interface()); err != nil {
					return err
				}
			}
			continue
		}
		if name, ok := tagValue(ft, "path"); ok {
			if err := setValue(c, f, name, true); err != nil {
				return err
			}
			continue
		}
		// 逃生舱 1：header 标签优先（独立于 query/form）
		if hname, hok := tagValue(ft, "header"); hok {
			if err := setRaw(f, c.GetHeader(hname), hname); err != nil {
				return err
			}
			continue
		}
		name, ok := tagValue(ft, "query")
		if !ok {
			name, ok = tagValue(ft, "form")
		}
		if !ok {
			continue
		}
		if err := setValue(c, f, name, false); err != nil {
			return err
		}
		if strings.Contains(ft.Tag.Get("binding"), "required") && f.IsZero() {
			return fmt.Errorf("%s is required", name)
		}
	}
	return nil
}

func tagValue(ft reflect.StructField, key string) (string, bool) {
	v := ft.Tag.Get(key)
	if v == "" {
		return "", false
	}
	return strings.Split(v, ",")[0], true
}

// setValue 从 query/path 取值并写入字段
func setValue(c *gin.Context, f reflect.Value, name string, path bool) error {
	raw := c.Query(name)
	if path {
		raw = c.Param(name)
	}
	// 多值 query（?tag=a&tag=b）→ []string
	if f.Kind() == reflect.Slice && !path && f.Type().Elem().Kind() == reflect.String {
		if vals := c.QueryArray(name); len(vals) > 0 {
			f.Set(reflect.ValueOf(vals))
			return nil
		}
	}
	return setRaw(f, raw, name)
}

// setRaw 把原始字符串解析写入字段（query/path/header 共用）
func setRaw(f reflect.Value, raw, name string) error {
	if raw == "" {
		return nil
	}
	switch f.Kind() {
	case reflect.String:
		f.SetString(raw)
	case reflect.Bool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("invalid %s: %s", name, raw)
		}
		f.SetBool(v)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid %s: %s", name, raw)
		}
		f.SetInt(v)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid %s: %s", name, raw)
		}
		f.SetUint(v)
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("invalid %s: %s", name, raw)
		}
		f.SetFloat(v)
	case reflect.Slice:
		if f.Type().Elem().Kind() == reflect.String {
			f.Set(reflect.ValueOf([]string{raw}))
		}
	}
	return nil
}
