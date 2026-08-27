// Package server 运行时适配器：把统一路由注册表(routes.All())挂载到原生 Gin。
// 职责：参数绑定 -> 入参转换 -> 校验 -> 上下文注入 -> Handler 调用 ->
// 出参转换 -> 状态码决策 -> 响应壳包装 -> 写出。
// 设计目标：业务层零框架依赖；解析/校验/错误映射/响应壳均可扩展。
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
	"sync"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/contract/response"
	"github.com/EdSan845D/oapi-hinge/contract/validator"

	"github.com/gin-gonic/gin"
)

// Server 运行时服务器装配器
type Server struct {
	middlewares []gin.HandlerFunc
	// 校验器：绑定后按注册顺序执行（内置标签必填校验 + Validate() 方法在绑定阶段完成）
	validators []validator.Func
	// 错误映射：默认 ErrNotFound -> 404，其余业务错误 -> 200 + code:7。
	// 优先级低于错误自带状态码（contract.StatusError / contract.StatusCoder）。
	mapError func(err error) (httpStatus, bizCode int)
	// 上下文装饰：把 gin 上下文中的用户/claims 注入 handler 的 context.Context
	decorate func(c *gin.Context, ctx context.Context) context.Context
	// 响应壳：成功/失败统一经过其包装（默认 {code, data, msg}）
	envelope response.Envelope
}

// New 创建 Server
func New() *Server {
	s := &Server{}
	s.mapError = defaultErrorMapper
	s.decorate = func(c *gin.Context, ctx context.Context) context.Context {
		return contract.WithFramework(ctx, c)
	}
	s.envelope = response.DefaultEnvelope{}
	return s
}

// Use 扩展点：挂载全局中间件（鉴权/CORS/限流等），在业务路由之前执行。
// 按分组挂载的中间件请直接写在 contract.Group.Middlewares（随树继承）。
func (s *Server) Use(mw ...gin.HandlerFunc) *Server {
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

// SetContextDecorator 扩展点：把 gin 上下文信息注入 handler 的 context.Context
func (s *Server) SetContextDecorator(fn func(c *gin.Context, ctx context.Context) context.Context) *Server {
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
	// 挂载期预计算反射信息（请求期零反射获取）
	h := reflect.ValueOf(r.Handler)
	qType := h.Type().In(1)
	bType := h.Type().In(2)
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
	fail := func(c *gin.Context, status, code int, msg string) {
		c.PureJSON(status, env.Failure(status, code, msg))
	}
	g.Handle(r.Method, ginPath(r.Path), func(c *gin.Context) {
		// Q：query/path 参数解析
		q := newValue(qType)
		if err := bindQueryPath(c, q.Interface()); err != nil {
			fail(c, http.StatusOK, CodeError, err.Error())
			return
		}

		// B：JSON body 解析（非 body 方法或无 body 路由跳过）
		b := newValue(bType)
		if isBodyMethod(r.Method) && bType.Kind() != reflect.Interface {
			if c.Request.Body != nil && c.Request.ContentLength > 0 {
				if err := c.ShouldBindJSON(b.Interface()); err != nil {
					fail(c, http.StatusOK, CodeError, err.Error())
					return
				}
				// 入参转换：绑定后、校验前
				if err := contract.TransformIn(c.Request.Context(), b.Interface()); err != nil {
					fail(c, http.StatusOK, CodeError, err.Error())
					return
				}
			}
		}

		// 校验：内置（标签 required + Validate() 接口）+ 自定义校验器
		if err := validator.Run(c.Request.Context(), r.Method, q.Interface(), b.Interface(), s.validators...); err != nil {
			fail(c, http.StatusOK, CodeError, err.Error())
			return
		}

		// 上下文注入
		ctx := s.decorate(c, c.Request.Context())

		out := h.Call([]reflect.Value{reflect.ValueOf(ctx), q.Elem(), b.Elem()})
		if ei := out[1].Interface(); ei != nil {
			err := ei.(error)
			status, code, msg := resolveError(s, err)
			fail(c, status, code, msg)
			return
		}

		// 出参转换 + 逃生舱 2 解包（响应定制壳 Status/Headers/Cookies）
		status := successStatus
		respVal := out[0]
		if w, ok := respVal.Interface().(contract.ResponseWrapper); ok {
			if w.ResponseStatus() != 0 {
				status = w.ResponseStatus()
			}
			for k, v := range w.ResponseHeaders() {
				c.Header(k, v)
			}
			for _, cookie := range w.ResponseCookies() {
				http.SetCookie(c.Writer, cookie)
			}
			respVal = reflect.ValueOf(w.ResponseData())
		}
		// 出参转换：序列化之前（脱敏/裁剪/补充字段）
		tv, err := contract.TransformOut(c.Request.Context(), respVal)
		if err != nil {
			status, code, msg := resolveError(s, err)
			fail(c, status, code, msg)
			return
		}
		respAny := tv.Interface()

		switch v := respAny.(type) {
		case *contract.FileStream:
			if v == nil {
				fail(c, http.StatusNotFound, http.StatusNotFound, "file not found")
				return
			}
			serveFile(c, v)
		case contract.Empty:
			c.PureJSON(status, env.Success(status, nil))
		default:
			c.PureJSON(status, env.Success(status, respAny))
		}
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
				code = CodeError
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
		return status, CodeError, err.Error()
	}
	status, code := s.mapError(err)
	return status, code, err.Error()
}

// 默认错误映射：ErrNotFound -> 404；其余业务错误 -> HTTP 200 + code:7
// （如需 RESTful 语义（如 400/422），用 SetErrorMapper 覆盖或返回 StatusError）
func defaultErrorMapper(err error) (int, int) {
	if errors.Is(err, contract.ErrNotFound) {
		return http.StatusNotFound, http.StatusNotFound
	}
	return http.StatusOK, CodeError
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

// fieldMeta 绑定字段元数据（挂载期解析一次，请求期零反射）
type fieldMeta struct {
	index    int
	kind     reflect.Kind
	path     string
	query    string
	form     string
	header   string
	required bool
	children []fieldMeta
}

// bindCache Q 类型 -> 字段元数据缓存
var bindCache sync.Map

// parseFields 反射解析结构体字段元数据（内嵌结构体递归展平）
func parseFields(t reflect.Type) []fieldMeta {
	if v, ok := bindCache.Load(t); ok {
		return v.([]fieldMeta)
	}
	var out []fieldMeta
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Anonymous {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				out = append(out, fieldMeta{children: parseFields(ft)})
			}
			continue
		}
		var m fieldMeta
		m.index = i
		m.kind = f.Type.Kind()
		m.path, _ = tagValue(f, "path")
		m.query, _ = tagValue(f, "query")
		m.form, _ = tagValue(f, "form")
		m.header, _ = tagValue(f, "header")
		out = append(out, m)
	}
	bindCache.Store(t, out)
	return out
}

// bindQueryPath 按预解析的字段元数据绑定（query/form/path/header 标签）。
// 必填校验统一在 validator.Run 执行（binding/validate 双标签），此处不再重复。
func bindQueryPath(c *gin.Context, req any) error {
	rv := reflect.ValueOf(req)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return errors.New("invalid params type")
	}
	return bindFields(c, rv.Elem(), parseFields(rv.Elem().Type()))
}

// bindFields 按元数据逐字段绑定
func bindFields(c *gin.Context, e reflect.Value, metas []fieldMeta) error {
	for _, m := range metas {
		f := e.Field(m.index)
		if len(m.children) > 0 {
			sub := f
			if sub.Kind() == reflect.Pointer {
				if sub.IsNil() {
					sub.Set(reflect.New(sub.Type().Elem()))
				}
				sub = sub.Elem()
			}
			if err := bindFields(c, sub, m.children); err != nil {
				return err
			}
			continue
		}
		if m.path != "" {
			if err := setRaw(f, c.Param(m.path), m.path); err != nil {
				return err
			}
			continue
		}
		// 逃生舱 1：header 标签优先（独立于 query/form）
		if m.header != "" {
			if err := setRaw(f, c.GetHeader(m.header), m.header); err != nil {
				return err
			}
			continue
		}
		if m.query != "" || m.form != "" {
			name := m.query
			if name == "" {
				name = m.form
			}
			if err := setValue(c, f, name, false); err != nil {
				return err
			}
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
