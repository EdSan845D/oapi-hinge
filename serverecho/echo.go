// Package serverecho echo 运行时适配器：把统一路由分组树(routes.All())挂载到原生 Echo。
// 与 servergin（gin 适配器）能力等价：参数绑定 -> 入参转换 -> 校验 -> 上下文注入 ->
// Handler 调用 -> 出参转换 -> 状态码决策 -> 响应壳包装 -> 写出。
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

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/contract/response"
	"github.com/EdSan845D/oapi-hinge/contract/validator"

	"github.com/labstack/echo/v4"
)

// codeError 业务错误码（与 contract/response.CodeError 一致，适配器内部使用）
const codeError = 7

// Server echo 运行时服务器装配器
type Server struct {
	middlewares []echo.MiddlewareFunc
	validators  []validator.Func
	mapError    func(err error) (httpStatus, bizCode int)
	decorate    func(c echo.Context, ctx context.Context) context.Context
	envelope    response.Envelope
}

// New 创建 Server
func New() *Server {
	s := &Server{}
	s.mapError = defaultErrorMapper
	s.decorate = func(c echo.Context, ctx context.Context) context.Context {
		return contract.WithFramework(ctx, c)
	}
	s.envelope = response.DefaultEnvelope{}
	return s
}

// Use 扩展点：挂载全局中间件（在业务路由之前执行）
func (s *Server) Use(mw ...echo.MiddlewareFunc) *Server {
	s.middlewares = append(s.middlewares, mw...)
	return s
}

// AddValidator 扩展点：注册自定义校验器（绑定后执行），
// 见 validator 包：内置标签必填 + Validate() 接口调用；validator.Playground() 接入完整校验规则
func (s *Server) AddValidator(v validator.Func) *Server {
	s.validators = append(s.validators, v)
	return s
}

// SetErrorMapper 扩展点：自定义 错误 -> (HTTP状态码, 业务code) 映射。
// 仅对不携带状态码的普通错误生效（StatusError / StatusCoder 优先）。
func (s *Server) SetErrorMapper(fn func(err error) (httpStatus, bizCode int)) *Server {
	s.mapError = fn
	return s
}

// SetContextDecorator 扩展点：把 echo 上下文信息注入 handler 的 context.Context
func (s *Server) SetContextDecorator(fn func(c echo.Context, ctx context.Context) context.Context) *Server {
	s.decorate = fn
	return s
}

// SetEnvelope 扩展点：自定义响应壳。
// 传入 nil 恢复默认壳 {code, data, msg}；路由级覆盖见 contract.RouteMeta.Envelope。
// 文档侧请用 openapi.OptionWithEnvelopeSchema 配对配置（两者独立）。
func (s *Server) SetEnvelope(env response.Envelope) *Server {
	if env != nil {
		s.envelope = env
	}
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

		// 响应壳：路由级覆盖 > 服务级
		env := s.envelope
		if r.Envelope != nil {
			env = r.Envelope
		}
		// 成功默认状态码：路由级 > 200
		successStatus := r.DefaultStatusCode
		if successStatus == 0 {
			successStatus = http.StatusOK
		}
		fail := func(status, code int, msg string) error {
			return c.JSON(status, env.Failure(status, code, msg))
		}

		// Q：query/path 参数解析。
		// Q 为接口类型（contract.NoReq / any 等占位）时视为无入参：跳过绑定与校验，
		// handler 收到 nil；具体结构体按标签绑定后进入校验流程。
		qArg := reflect.New(t.In(1)).Elem()
		if t.In(1).Kind() != reflect.Interface {
			q := newValue(t.In(1))
			if err := bindQueryPath(c, q.Interface()); err != nil {
				return fail(http.StatusOK, codeError, err.Error())
			}
			qArg = q.Elem()
		}

		// B：JSON body 解析（非 body 方法、无 body 或接口占位 any 时跳过）
		bArg := reflect.New(t.In(2)).Elem()
		if isBodyMethod(r.Method) && t.In(2).Kind() != reflect.Interface {
			b := newValue(t.In(2))
			if c.Request().Body != nil && c.Request().ContentLength > 0 {
				if err := c.Bind(b.Interface()); err != nil {
					return fail(http.StatusOK, codeError, err.Error())
				}
				// 入参转换：绑定后、校验前
				if err := contract.TransformIn(c.Request().Context(), b.Interface()); err != nil {
					return fail(http.StatusOK, codeError, err.Error())
				}
				bArg = b.Elem()
			}
		}

		// 校验：内置（标签 required + Validate() 接口）+ 自定义校验器
		if err := validator.Run(c.Request().Context(), r.Method, checkTarget(t.In(1), qArg), checkTarget(t.In(2), bArg), s.validators...); err != nil {
			return fail(http.StatusOK, codeError, err.Error())
		}

		// 上下文注入
		ctx := s.decorate(c, c.Request().Context())

		out := h.Call([]reflect.Value{reflect.ValueOf(ctx), qArg, bArg})
		if ei := out[1].Interface(); ei != nil {
			err := ei.(error)
			status, code, msg := resolveError(s, err)
			return fail(status, code, msg)
		}

		// 出参转换 + 逃生舱 2 解包（响应定制壳 Status/Headers/Cookies）
		status := successStatus
		respVal := out[0]
		if w, ok := respVal.Interface().(contract.ResponseWrapper); ok {
			if w.ResponseStatus() != 0 {
				status = w.ResponseStatus()
			}
			for k, v := range w.ResponseHeaders() {
				c.Response().Header().Set(k, v)
			}
			for _, cookie := range w.ResponseCookies() {
				c.SetCookie(cookie)
			}
			respVal = reflect.ValueOf(w.ResponseData())
		}
		// 出参转换：序列化之前（脱敏/裁剪/补充字段）
		tv, err := contract.TransformOut(c.Request().Context(), respVal)
		if err != nil {
			status, code, msg := resolveError(s, err)
			return fail(status, code, msg)
		}
		respAny := tv.Interface()

		// 写出：FileStream 直接输出流；其余（含 Empty/any 占位、nil 数据）统一壳写出。
		// 注意：Empty 已为接口类型（defined type any），不能作为 type switch 分支——
		// 接口 case 会匹配一切实现（吞掉全部非流响应），必须显式判断。
		if f, ok := respAny.(*contract.FileStream); ok {
			if f == nil {
				return fail(http.StatusNotFound, http.StatusNotFound, "file not found")
			}
			return serveFile(c, f)
		}
		return c.JSON(status, env.Success(status, respAny))
	})
}

// resolveError 错误 → (HTTP状态码, 业务code, 对外信息)。
// 优先级：contract.StatusError（自带状态码/业务码/信息）→ contract.StatusCoder（仅状态码）
// → SetErrorMapper 全局映射（存量行为）。
func resolveError(s *Server, err error) (int, int, string) {
	var se *contract.StatusError
	if errors.As(err, &se) {
		status := se.StatusCode()
		code := se.Code
		if code == 0 {
			if status == http.StatusOK {
				code = codeError
			} else {
				code = status
			}
		}
		msg := se.Msg
		if msg == "" {
			msg = err.Error()
		}
		return status, code, msg
	}
	var sc contract.StatusCoder
	if errors.As(err, &sc) {
		status := sc.StatusCode()
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return status, codeError, err.Error()
	}
	status, code := s.mapError(err)
	return status, code, err.Error()
}

// 默认错误映射：ErrNotFound -> 404；其余业务错误 -> HTTP 200 + code:7
func defaultErrorMapper(err error) (int, int) {
	if errors.Is(err, contract.ErrNotFound) {
		return http.StatusNotFound, http.StatusNotFound
	}
	return http.StatusOK, codeError
}

// checkTarget 校验入参目标：接口类型占位（NoReq/any 等）返回 nil（validator.isNil 跳过）；
// 具体结构体返回其指针（与绑定阶段一致）
func checkTarget(t reflect.Type, v reflect.Value) any {
	if t.Kind() == reflect.Interface {
		return nil
	}
	return v.Addr().Interface()
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

// bindQueryPath 反射遍历 Q 结构体，绑定 query/form 与 path 标签字段（内嵌结构体递归展平）。
// 必填校验统一在 validator.Run 执行（binding/validate 双标签），此处不再重复。
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
