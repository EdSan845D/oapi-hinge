package servergin

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/contract/validator"
	"github.com/gin-gonic/gin"
)

func (s *Server) mountGroup(parent *gin.RouterGroup, grp *contract.Group) {
	sub := parent.Group(grp.Prefix)
	for _, mw := range grp.Middlewares {
		switch fn := mw.(type) {
		case gin.HandlerFunc:
			sub.Use(fn)
		case func(*gin.Context):
			sub.Use(gin.HandlerFunc(fn))
		default:
			// 静默忽略会让人误以为中间件已生效；挂载期直接报错并指明位置
			panic(fmt.Sprintf("servergin: group %q middleware type %T is not gin.HandlerFunc/func(*gin.Context)", grp.Prefix, mw))
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
	if err := contract.CheckHandler(r.Handler); err != nil {
		// 挂载期签名校验：反射调用期的 panic 信息晦涩，这里给出路由定位
		panic(fmt.Sprintf("servergin: mount %s %s: invalid handler: %v", r.Method, r.Path, err))
	}
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
	// 绑定/校验失败统一走 bindStatus（默认 200；SetBindErrorStatus 可调）
	bindStatus, bindCode := bindFail(s.bindStatus)
	g.Handle(r.Method, ginPath(r.Path), func(c *gin.Context) {
		// 上下文装饰：最先执行（Q/B 绑定之前），TransformIn / 校验器 / TransformOut /
		// handler 全程共享同一个已装饰 ctx（用户、租户、依赖注入等）
		ctx := s.decorate(c, c.Request.Context())

		// Q：query/path 参数解析。
		// Q 为接口类型（contract.NoReq / any 等占位）时视为无入参：跳过绑定与校验，
		// handler 收到 nil；具体结构体按标签绑定后进入校验流程。
		qArg := reflect.New(qType).Elem()
		if qType.Kind() != reflect.Interface {
			q := contract.NewValue(qType)
			if err := bindQueryPath(c, q.Interface()); err != nil {
				fail(c, bindStatus, bindCode, err.Error())
				return
			}
			// 入参转换（Q）：与 B 一致，绑定后、校验前自动调用
			if err := contract.TransformIn(ctx, q.Interface()); err != nil {
				fail(c, bindStatus, bindCode, err.Error())
				return
			}
			qArg = q.Elem()
		}

		// B：JSON body 解析（非 body 方法、无 body 或接口占位 any 时跳过）
		bArg := reflect.New(bType).Elem()
		if contract.IsBodyMethod(r.Method) && bType.Kind() != reflect.Interface {
			b := contract.NewValue(bType)
			if c.Request.Body != nil && c.Request.ContentLength > 0 {
				if err := c.ShouldBindJSON(b.Interface()); err != nil {
					fail(c, bindStatus, bindCode, err.Error())
					return
				}
				// 入参转换：绑定后、校验前；ctx 为已装饰上下文（含用户/框架信息）
				if err := contract.TransformIn(ctx, b.Interface()); err != nil {
					fail(c, bindStatus, bindCode, err.Error())
					return
				}
				bArg = b.Elem()
			}
		}

		// 校验：内置（标签 required + Validate() 接口）+ 自定义校验器（可读装饰 ctx）
		if err := validator.Run(ctx, r.Method, contract.CheckTarget(qType, qArg), contract.CheckTarget(bType, bArg), s.validators...); err != nil {
			fail(c, bindStatus, bindCode, err.Error())
			return
		}

		out := h.Call([]reflect.Value{reflect.ValueOf(ctx), qArg, bArg})
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
		tv, err := contract.TransformOut(ctx, respVal)
		if err != nil {
			status, code, msg := resolveError(s, err)
			fail(c, status, code, msg)
			return
		}
		respAny := tv.Interface()

		// 写出：FileStream 直接输出流；其余（含 Empty/any 占位、nil 数据）统一壳写出。
		// 注意：Empty 已为接口类型（defined type any），不能作为 type switch 分支——
		// 接口 case 会匹配一切实现（吞掉全部非流响应），必须显式判断。
		switch fv := respAny.(type) {
		case *contract.FileStream:
			if fv == nil {
				fail(c, http.StatusNotFound, http.StatusNotFound, "file not found")
				return
			}
			serveFile(c, fv)
			return
		case contract.FileStream:
			// 值类型同样按流输出（否则会被当作 JSON 对象序列化）
			serveFile(c, &fv)
			return
		}
		c.PureJSON(status, env.Success(status, respAny))
	})
}

// resolveError 错误 → (HTTP状态码, 业务code, 对外信息)。
// 优先级：contract.StatusError（自带状态码/业务码/信息）→ contract.StatusCoder（仅状态码）
// → SetErrorMapper 全局映射（存量行为）。
func resolveError(s *Server, err error) (int, int, string) {
	if se, ok := errors.AsType[*contract.StatusError](err); ok {
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
	if sc, ok := errors.AsType[contract.StatusCoder](err); ok {
		status := sc.StatusCode()
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return status, CodeError, err.Error()
	}
	status, code := s.mapError(err)
	return status, code, err.Error()
}

// bindFail 绑定/校验失败响应的 (status, code)：默认 200 + CodeError（存量行为）；
// 自定义非 200 状态码时 code 跟随状态码（与 StatusError 约定一致）
func bindFail(status int) (int, int) {
	if status <= 0 || status == http.StatusOK {
		return http.StatusOK, CodeError
	}
	return status, status
}

// 默认错误映射：ErrNotFound -> 404；其余业务错误 -> HTTP 200 + code:7
// （如需 RESTful 语义（如 400/422），用 SetErrorMapper 覆盖或返回 StatusError）
func defaultErrorMapper(err error) (int, int) {
	if errors.Is(err, contract.ErrNotFound) {
		return http.StatusNotFound, http.StatusNotFound
	}
	return http.StatusOK, CodeError
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
	// 文件名缺省时不输出 Content-Disposition（避免空文件名的非法头）
	if f.Name != "" {
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(f.Name)))
	}
	if f.Size > 0 {
		c.DataFromReader(http.StatusOK, f.Size, contentType, f.Reader, nil)
		return
	}
	// Size 未知（<=0）：分块传输。DataFromReader 会写入 Content-Length，
	// 长度不符会截断/挂起响应，这里改用 io.Copy
	c.Header("Content-Type", contentType)
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, f.Reader)
}
