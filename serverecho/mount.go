package serverecho

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/contract/validator"

	"github.com/labstack/echo/v4"
)

func (s *Server) mountGroup(parent *echo.Group, grp *contract.Group) {
	sub := parent.Group(grp.Prefix)
	for _, mw := range grp.Middlewares {
		if fn, ok := mw.(echo.MiddlewareFunc); ok {
			sub.Use(fn)
			continue
		}
		// 静默忽略会让人误以为中间件已生效；挂载期直接报错并指明位置
		panic(fmt.Sprintf("serverecho: group %q middleware type %T is not echo.MiddlewareFunc", grp.Prefix, mw))
	}
	for _, r := range grp.Routes {
		s.mount(sub, r)
	}
	for _, child := range grp.Children {
		s.mountGroup(sub, child)
	}
}

func (s *Server) mount(g *echo.Group, r contract.Route) {
	if err := contract.CheckHandler(r.Handler); err != nil {
		// 挂载期签名校验：反射调用期的 panic 信息晦涩，这里给出路由定位
		panic(fmt.Sprintf("serverecho: mount %s %s: invalid handler: %v", r.Method, r.Path, err))
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
	fail := func(c echo.Context, status, code int, msg string) error {
		return c.JSON(status, env.Failure(status, code, msg))
	}
	g.Add(r.Method, echoPath(r.Path), func(c echo.Context) error {
		// 上下文装饰：最先执行（Q/B 绑定之前），TransformIn / 校验器 /
		// TransformOut / handler 全程共享同一个已装饰 ctx。
		// 约定：decorate 必须是纯派生（只读 c、轻量 WithValue 级操作），
		// 重操作（鉴权、查库）放中间件——每个请求（含校验失败的请求）都会执行。
		ctx := s.decorate(c, c.Request().Context())

		// Q：query/path 参数解析。
		// Q 为接口类型（contract.NoReq / any 等占位）时视为无入参：跳过绑定与校验，
		// handler 收到 nil；具体结构体按标签绑定后进入校验流程。
		qArg := reflect.New(qType).Elem()
		if qType.Kind() != reflect.Interface {
			q := contract.NewValue(qType)
			if err := bindQueryPath(c, q.Interface()); err != nil {
				st, cd, msg := s.bindError(err)
				return fail(c, st, cd, msg)
			}
			// 入参转换（Q）：与 B 一致，绑定后、校验前自动调用；ctx 为已装饰上下文
			if err := contract.TransformIn(ctx, q.Interface()); err != nil {
				st, cd, msg := s.bindError(err)
				return fail(c, st, cd, msg)
			}
			qArg = q.Elem()
		}

		// B：JSON body 解析（非 body 方法、无 body 或接口占位 any 时跳过）
		bArg := reflect.New(bType).Elem()
		if contract.IsBodyMethod(r.Method) && bType.Kind() != reflect.Interface {
			b := contract.NewValue(bType)
			if c.Request().Body != nil && c.Request().ContentLength > 0 {
				// 与 gin 适配器对齐：固定 JSON 解码（echo 的 c.Bind 按 Content-Type 分发，
				// 非 JSON Content-Type 会走 form 绑定，同一份路由表两端行为不一致）
				if err := json.NewDecoder(c.Request().Body).Decode(b.Interface()); err != nil {
					st, cd, msg := s.bindError(err)
					return fail(c, st, cd, msg)
				}
				// 入参转换：绑定后、校验前；ctx 为已装饰上下文（含用户/框架信息）
				if err := contract.TransformIn(ctx, b.Interface()); err != nil {
					st, cd, msg := s.bindError(err)
					return fail(c, st, cd, msg)
				}
				bArg = b.Elem()
			}
		}

		// 校验：内置（标签 required + Validate() 接口）+ 自定义校验器（可读装饰 ctx）
		if err := validator.Run(ctx, r.Method, contract.CheckTarget(qType, qArg), contract.CheckTarget(bType, bArg), s.validators...); err != nil {
			st, cd, msg := s.bindError(err)
			return fail(c, st, cd, msg)
		}

		out := h.Call([]reflect.Value{reflect.ValueOf(ctx), qArg, bArg})
		if ei := out[1].Interface(); ei != nil {
			err := ei.(error)
			status, code, msg := resolveError(s, err)
			return fail(c, status, code, msg)
		}

		// 出参转换 + 响应定制解包（contract.Response[R] 的 Status/Headers/Cookies）
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
		tv, err := contract.TransformOut(ctx, respVal)
		if err != nil {
			status, code, msg := resolveError(s, err)
			return fail(c, status, code, msg)
		}
		respAny := tv.Interface()

		// 写出：FileStream 直接输出流；其余（含 Empty/any 占位、nil 数据）统一壳写出。
		// 注意：Empty 已为接口类型（defined type any），不能作为 type switch 分支——
		// 接口 case 会匹配一切实现（吞掉全部非流响应），必须显式判断。
		switch fv := respAny.(type) {
		case *contract.FileStream:
			if fv == nil {
				return fail(c, http.StatusNotFound, http.StatusNotFound, "file not found")
			}
			return serveFile(c, fv)
		case contract.FileStream:
			// 值类型同样按流输出（否则会被当作 JSON 对象序列化）
			return serveFile(c, &fv)
		}
		return c.JSON(status, env.Success(status, respAny))
	})
}

// resolveError 业务错误 → (HTTP状态码, 业务code, 对外信息)。
// 默认策略在 contract 层（contract.ResolveError）：错误自带状态码优先 → SetErrorMapper 兜底。
func resolveError(s *Server, err error) (int, int, string) {
	return contract.ResolveError(s.mapError, err)
}

// bindError 绑定/校验阶段错误（Q/B 绑定、TransformIn、校验器）的状态决策：
// 携带状态码的错误（如 ParamBinder 返回 NotFound）走统一错误链；
// 普通错误保持 bindStatus 语义（默认 200 + codeError 存量行为，SetBindErrorStatus 可调）。
func (s *Server) bindError(err error) (int, int, string) {
	if status, code, msg, ok := contract.ResolveErrorStatus(err); ok {
		return status, code, msg
	}
	status, code := contract.BindFail(s.bindStatus)
	return status, code, err.Error()
}
func echoPath(p string) string {
	return strings.NewReplacer("{", ":", "}", "").Replace(p)
}

// serveFile 输出二进制流（数据源为 io.Reader）
func serveFile(c echo.Context, f *contract.FileStream) error {
	contentType := f.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// 文件名缺省时不输出 Content-Disposition（避免空文件名的非法头）
	if f.Name != "" {
		c.Response().Header().Set("Content-Disposition",
			fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(f.Name)))
	}
	// Size 已知时显式声明 Content-Length；未知（<=0）走 echo Stream 的分块传输，
	// 避免错误 Content-Length 截断响应
	if f.Size > 0 {
		c.Response().Header().Set(echo.HeaderContentLength, strconv.FormatInt(f.Size, 10))
	}
	return c.Stream(http.StatusOK, contentType, f.Reader)
}
