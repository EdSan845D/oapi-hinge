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
	"github.com/EdSan845D/oapi-hinge/contract/response"
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
	// multipart 契约校验：FileHeader 字段缺 form 标签会让文件静默丢失，挂载期报错
	if contract.HasFileHeader(bType) {
		if err := contract.CheckMultipartTags(bType); err != nil {
			panic(fmt.Sprintf("servergin: mount %s %s: %v", r.Method, r.Path, err))
		}
	}
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
		// 关联 ID：开启后沿用入站 X-Correlation-Id（缺失则生成 UUIDv4），
		// 注入请求 ctx 并回写响应头；先于业务 decorator，decorator 与 handler 均可读取。
		if s.correlation {
			cid := c.GetHeader(contract.HeaderCorrelationID)
			if cid == "" {
				cid = contract.NewCorrelationID()
			}
			c.Header(contract.HeaderCorrelationID, cid)
			c.Request = c.Request.WithContext(contract.WithCorrelationID(c.Request.Context(), cid))
		}
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
				st, cd, msg := s.bindError(err)
				fail(c, st, cd, msg)
				return
			}
			// 入参转换（Q）：与 B 一致，绑定后、校验前自动调用
			if err := contract.TransformIn(ctx, q.Interface()); err != nil {
				st, cd, msg := s.bindError(err)
				fail(c, st, cd, msg)
				return
			}
			qArg = q.Elem()
		}

		// B：按静态类型分派——RawBody 原始字节 / multipart 文件表单 / JSON（默认）。
		// 非 body 方法或接口占位（any/NoReq）时跳过绑定，handler 收到零值。
		bArg := reflect.New(bType).Elem()
		if contract.IsBodyMethod(r.Method) && bType.Kind() != reflect.Interface {
			b := contract.NewValue(bType)
			bound := false
			switch {
			case bType == rawBodyType:
				// 原始请求体：零解码整包字节交给业务层（webhook 验签/自定义编码）
				data, err := io.ReadAll(c.Request.Body)
				if err != nil {
					st, cd, msg := s.bindError(err)
					fail(c, st, cd, msg)
					return
				}
				b.Elem().Set(reflect.ValueOf(contract.RawBody(data)))
				bound = true
			case contract.HasFileHeader(bType):
				// multipart 文件表单：标准库解析（gin/echo 行为一致），按 form 标签填充。
				// 内存缓冲水位固定 32MB；应用需自定义时在中间件里先 ParseMultipartForm(n)，
				// 标准库对已解析请求为空操作，此处调用自动退化为 no-op。
				if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
					st, cd, msg := s.bindError(err)
					fail(c, st, cd, msg)
					return
				}
				if err := contract.BindMultipart(c.Request.MultipartForm, b.Interface()); err != nil {
					st, cd, msg := s.bindError(err)
					fail(c, st, cd, msg)
					return
				}
				bound = true
			default:
				if c.Request.Body != nil && c.Request.ContentLength > 0 {
					if err := c.ShouldBindJSON(b.Interface()); err != nil {
						st, cd, msg := s.bindError(err)
						fail(c, st, cd, msg)
						return
					}
					bound = true
				}
			}
			if bound {
				// 入参转换：绑定后、校验前；ctx 为已装饰上下文（含用户/框架信息）
				if err := contract.TransformIn(ctx, b.Interface()); err != nil {
					st, cd, msg := s.bindError(err)
					fail(c, st, cd, msg)
					return
				}
				bArg = b.Elem()
			}
		}

		// 校验：内置（标签 required + Validate() 接口）+ 自定义校验器（可读装饰 ctx）
		if err := validator.Run(ctx, r.Method, contract.CheckTarget(qType, qArg), contract.CheckTarget(bType, bArg), s.validators...); err != nil {
			st, cd, msg := s.bindError(err)
			fail(c, st, cd, msg)
			return
		}

		out := h.Call([]reflect.Value{reflect.ValueOf(ctx), qArg, bArg})
		if ei := out[1].Interface(); ei != nil {
			err := ei.(error)
			status, code, msg := resolveError(s, err)
			failAggregate(c, env, status, code, msg, err)
			return
		}

		// 出参转换 + 响应定制解包（contract.Response[R] 的 Status/Headers/Cookies）
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
			failAggregate(c, env, status, code, msg, err)
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

// resolveError 业务错误 → (HTTP状态码, 业务code, 对外信息)。
// 默认策略在 contract 层（contract.ResolveError）：错误自带状态码优先 → SetErrorMapper 兜底。
func resolveError(s *Server, err error) (int, int, string) {
	return contract.ResolveError(s.mapError, err)
}

// failAggregate 失败输出：错误链携带 contract.AggregateError 且壳实现
// response.AggregateEnvelope 时，额外输出逐项失败明细（aggregated_error）；
// 其余情况与普通失败完全一致（含未实现聚合接口的自定义壳）。
func failAggregate(c *gin.Context, env response.Envelope, status, code int, msg string, err error) {
	var agg *contract.AggregateError
	if errors.As(err, &agg) {
		if ae, ok := env.(response.AggregateEnvelope); ok {
			c.PureJSON(status, ae.AggregateFailure(status, code, msg, agg.Failed))
			return
		}
	}
	c.PureJSON(status, env.Failure(status, code, msg))
}

var rawBodyType = reflect.TypeOf(contract.RawBody(nil))

// bindError 绑定/校验阶段错误（Q/B 绑定、TransformIn、校验器）的状态决策：
// 携带状态码的错误（如 ParamBinder 返回 NotFound）走统一错误链；
// 普通错误保持 bindStatus 语义（默认 200 + CodeError 存量行为，SetBindErrorStatus 可调）。
func (s *Server) bindError(err error) (int, int, string) {
	if status, code, msg, ok := contract.ResolveErrorStatus(err); ok {
		return status, code, msg
	}
	status, code := contract.BindFail(s.bindStatus)
	return status, code, err.Error()
}

// ginPath 把 OpenAPI 风格路径 /users/{id} 转为 Gin 风格 /users/:id
func ginPath(p string) string {
	return strings.NewReplacer("{", ":", "}", "").Replace(p)
}

// serveFile 输出二进制流。
// Reader 实现 io.ReadSeeker 且 Size>0 时走 http.ServeContent：自动支持
// Range/206 多段、If-None-Match / If-Modified-Since / If-Range 条件请求与 416；
// 其余情况回退全量输出（与旧版行为一致）。
func serveFile(c *gin.Context, f *contract.FileStream) {
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
	_, _ = io.Copy(c.Writer, f.Reader)
}
