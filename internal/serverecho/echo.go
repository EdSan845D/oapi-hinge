// Package serverecho echo 运行时适配器：把统一路由分组树(routes.All())挂载到原生 Echo。
// 与 internal/server（gin 适配器）能力等价：参数绑定 -> 校验 -> 上下文注入 ->
// Handler 调用 -> 统一响应；响应壳与 gin 版保持同一格式 {code, data, msg}。
// 中间件按函数类型断言：echo 项目在 Group.Middlewares 中放 echo.MiddlewareFunc。
package serverecho

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
	"fuego-hinge/internal/validator"

	"github.com/labstack/echo/v4"
)

// 统一响应壳（与 internal/response 同格式，避免 response 包依赖 gin）
type envelope struct {
	Code int    `json:"code"`
	Data any    `json:"data"`
	Msg  string `json:"msg"`
}

// Server echo 运行时服务器装配器
type Server struct {
	middlewares []echo.MiddlewareFunc
	validators  []validator.Func
	mapError    func(err error) (httpStatus, bizCode int)
	decorate    func(c echo.Context, ctx context.Context) context.Context
}

// New 创建 Server
func New() *Server {
	s := &Server{}
	s.mapError = defaultErrorMapper
	s.decorate = func(c echo.Context, ctx context.Context) context.Context {
		return contract.WithFramework(ctx, c)
	}
	return s
}

// Use 扩展点：挂载全局中间件（在业务路由之前执行）
func (s *Server) Use(mw ...echo.MiddlewareFunc) *Server {
	s.middlewares = append(s.middlewares, mw...)
	return s
}

// AddValidator 扩展点：注册自定义校验器（绑定后执行）
func (s *Server) AddValidator(v validator.Func) *Server {
	s.validators = append(s.validators, v)
	return s
}

// SetErrorMapper 扩展点：自定义 错误 -> (HTTP状态码, 业务code) 映射
func (s *Server) SetErrorMapper(fn func(err error) (httpStatus, bizCode int)) *Server {
	s.mapError = fn
	return s
}

// SetContextDecorator 扩展点：把 echo 上下文信息注入 handler 的 context.Context
func (s *Server) SetContextDecorator(fn func(c echo.Context, ctx context.Context) context.Context) *Server {
	s.decorate = fn
	return s
}

// Mount 把路由分组树挂载到 echo。
// 组中间件就地断言为 echo.MiddlewareFunc 后 Use（echo 自动向子组继承）。
func (s *Server) Mount(g *echo.Group, groups []*contract.Group) {
	if len(s.middlewares) > 0 {
		g.Use(s.middlewares...)
	}
	for _, grp := range groups {
		s.mountGroup(g, grp)
	}
}

func (s *Server) mountGroup(parent *echo.Group, grp *contract.Group) {
	sub := parent.Group(grp.Prefix)
	for _, mw := range grp.Middlewares {
		if fn, ok := mw.(echo.MiddlewareFunc); ok {
			sub.Use(fn)
		}
	}
	for _, r := range grp.Routes {
		s.mount(sub, r)
	}
	for _, child := range grp.Children {
		s.mountGroup(sub, child)
	}
}

func (s *Server) mount(g *echo.Group, r contract.Route) {
	g.Add(r.Method, echoPath(r.Path), func(c echo.Context) error {
		h := reflect.ValueOf(r.Handler)
		t := h.Type()

		// Q：query/path 参数解析
		q := newValue(t.In(1))
		if err := bindQueryPath(c, q.Interface()); err != nil {
			return failWithMessage(c, err.Error())
		}

		// B：JSON body 解析（非 body 方法或无 body 路由跳过）
		b := newValue(t.In(2))
		if isBodyMethod(r.Method) && t.In(2).Kind() != reflect.Interface {
			if c.Request().Body != nil && c.Request().ContentLength > 0 {
				if err := c.Bind(b.Interface()); err != nil {
					return failWithMessage(c, err.Error())
				}

				// binding:"required" 标签校验（echo 的 Bind 不处理该标签，手动执行）
				if err := checkRequired(b.Interface()); err != nil {
					return failWithMessage(c, err.Error())
				}
			}
		}

		// 校验：内置（标签 required + Validate() 接口）+ 自定义校验器
		if err := validator.Run(c.Request().Context(), r.Method, q.Interface(), b.Interface(), s.validators...); err != nil {
			return failWithMessage(c, err.Error())
		}

		// 上下文注入
		ctx := s.decorate(c, c.Request().Context())

		out := h.Call([]reflect.Value{reflect.ValueOf(ctx), q.Elem(), b.Elem()})
		if ei := out[1].Interface(); ei != nil {
			err := ei.(error)
			status, code := s.mapError(err)
			if status != http.StatusOK {
				return c.JSON(status, echo.Map{"error": err.Error()})
			}
			return failWithCode(c, code, err.Error())
		}

		resp := out[0].Interface()
		// 逃生舱 2：响应定制壳（Status/Headers/Cookies）
		status := http.StatusOK
		if w, ok := resp.(contract.ResponseWrapper); ok {
			if w.ResponseStatus() != 0 {
				status = w.ResponseStatus()
			}
			for k, v := range w.ResponseHeaders() {
				c.Response().Header().Set(k, v)
			}
			for _, cookie := range w.ResponseCookies() {
				c.SetCookie(cookie)
			}
			resp = w.ResponseData()
		}
		switch v := resp.(type) {
		case *contract.FileStream:
			if v == nil {
				return failWithMessage(c, "file not found")
			}
			return serveFile(c, v)
		case contract.Empty:
			return okWithDataStatus(c, status, nil)
		default:
			return okWithDataStatus(c, status, resp)
		}
	})
}

// 默认错误映射：ErrNotFound -> 404；其余业务错误 -> HTTP 200 + code:7
func defaultErrorMapper(err error) (int, int) {
	if errors.Is(err, contract.ErrNotFound) {
		return http.StatusNotFound, http.StatusNotFound
	}
	return http.StatusOK, 7 // 与 response.CodeError 一致
}

func okWithData(c echo.Context, data any) error {
	return c.JSON(http.StatusOK, envelope{0, data, "操作成功"})
}

func okWithDataStatus(c echo.Context, status int, data any) error {
	return c.JSON(status, envelope{0, data, "操作成功"})
}

func failWithMessage(c echo.Context, msg string) error {
	return c.JSON(http.StatusOK, envelope{7, nil, msg})
}

func failWithCode(c echo.Context, code int, msg string) error {
	return c.JSON(http.StatusOK, envelope{code, nil, msg})
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

// echoPath 把 OpenAPI 风格路径 /users/{id} 转为 Echo 风格 /users/:id。
// 空路径保持原样：echo 的 Group.Add 用 prefix+path 拼接，空路径即组前缀本身。
func echoPath(p string) string {
	return strings.NewReplacer("{", ":", "}", "").Replace(p)
}

// serveFile 输出二进制流（数据源为 io.Reader）
func serveFile(c echo.Context, f *contract.FileStream) error {
	contentType := f.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Response().Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(f.Name)))
	return c.Stream(http.StatusOK, contentType, f.Reader)
}

// checkRequired 手动执行 body 结构体的 binding:"required" 标签校验
// （echo 的 Bind 只做反序列化，不识别 gin/validator 的 binding 标签）。
func checkRequired(req any) error {
	rv := reflect.ValueOf(req)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil
	}
	e := rv.Elem()
	if e.Kind() != reflect.Struct {
		return nil
	}
	var walk func(v reflect.Value) error
	walk = func(v reflect.Value) error {
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f, ft := v.Field(i), t.Field(i)
			if !ft.IsExported() {
				continue
			}
			if ft.Anonymous {
				sub := f
				if sub.Kind() == reflect.Pointer {
					if sub.IsNil() {
						continue
					}
					sub = sub.Elem()
				}
				if sub.Kind() == reflect.Struct {
					if err := walk(sub); err != nil {
						return err
					}
				}
				continue
			}
			if strings.Contains(ft.Tag.Get("binding"), "required") && f.IsZero() {
				name := strings.Split(ft.Tag.Get("json"), ",")[0]
				if name == "" || name == "-" {
					name = ft.Name
				}
				return fmt.Errorf("%s is required", name)
			}
		}
		return nil
	}
	return walk(e)
}

// bindQueryPath 反射遍历 Q 结构体，绑定 query/form 与 path 标签字段（内嵌结构体递归展平）
func bindQueryPath(c echo.Context, req any) error {
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
			if err := setRaw(f, c.Request().Header.Get(hname), hname); err != nil {
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
func setValue(c echo.Context, f reflect.Value, name string, path bool) error {
	var raw string
	if path {
		raw = c.Param(name)
	} else {
		raw = c.QueryParam(name)
	}
	// 多值 query（?tag=a&tag=b）→ []string
	if f.Kind() == reflect.Slice && !path && f.Type().Elem().Kind() == reflect.String {
		if vals := c.QueryParams()[name]; len(vals) > 0 {
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
