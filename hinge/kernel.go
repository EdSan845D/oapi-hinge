package hinge

import (
	"context"
	"errors"
	"net/http"
	"reflect"
)

// Kernel 框架无关的请求管线内核。装配期（Handle）完成拦截器解析、
// 响应壳选择与状态码预计算；请求期只做：装饰 ctx → 关联 ID → 拦截链 →
// 绑定（生成的 Binder）→ 校验 → 调用（生成的闭包）→ 出参转换 → 壳包装 → 写出。
// 全程除「值类型出参 + 指针接收者 OutTransform」拷贝外零反射。
type Kernel struct {
	envelope Envelope
	// bindStatus 绑定/校验失败的 HTTP 状态码（默认 200：HTTP 200 + code=7；
	// SetBindErrorStatus(400) 可获得 RESTful 语义，非 200 时 code 跟随状态码）。
	bindStatus int
	// correlation 关联 ID 注入开关（默认关闭）。
	correlation bool
	// mapError 错误映射：默认 ErrNotFound → 404，其余业务错误 → 200 + code:7。
	// 优先级低于错误自带状态码（StatusError / StatusCoder）。
	mapError func(err error) (httpStatus, bizCode int)
	// decorate 上下文装饰：请求期最前执行（Q/B 绑定之前），默认由框架适配器
	// 注入原生上下文对象；业务可追加（用户/租户/依赖注入）。
	decorate func(ctx context.Context, r RequestReader) context.Context
	// validators 自定义校验器：绑定后按注册顺序执行（生成的绑定器已含
	// 必填检查与 Validate() 调用；这里只跑注入的自定义校验器）。
	validators []ValidatorFunc
}

// ValidatorFunc 自定义校验器签名。q/b 为解析后的请求值（可能为 nil）。
type ValidatorFunc func(ctx context.Context, ep Endpoint, q, b any) error

// NewKernel 创建内核：默认壳 {code, data, msg}，默认错误映射，绑定失败 200。
func NewKernel() *Kernel {
	return &Kernel{
		envelope:   DefaultEnvelope{},
		bindStatus: http.StatusOK,
		mapError:   DefaultErrorMapper,
	}
}

// SetEnvelope 设置默认响应壳；nil 恢复默认壳。路由级覆盖见 Endpoint.Envelope
//（oapi:envelope 注解 + RegisterEnvelope 命名注册）。
func (k *Kernel) SetEnvelope(env Envelope) *Kernel {
	if env != nil {
		k.envelope = env
	}
	return k
}

// SetBindErrorStatus 设置绑定/校验失败（含 InTransform / Validate 错误）的 HTTP 状态码。
// 默认 200（HTTP 200 + code=CodeError）；设为 400 可获得 RESTful 语义，
// 非 200 时业务 code 跟随状态码。仅影响绑定/校验阶段；业务错误不受影响。
func (k *Kernel) SetBindErrorStatus(status int) *Kernel {
	if status > 0 {
		k.bindStatus = status
	}
	return k
}

// SetCorrelation 开启请求关联 ID（X-Correlation-Id）：入站沿用、缺失生成 UUIDv4，
// 注入请求 ctx 并回写响应头。默认关闭。
func (k *Kernel) SetCorrelation(enable bool) *Kernel {
	k.correlation = enable
	return k
}

// SetErrorMapper 自定义 错误 → (HTTP状态码, 业务code) 映射。
// 仅对不携带状态码的普通错误生效（StatusError / StatusCoder 优先）。
func (k *Kernel) SetErrorMapper(fn func(err error) (httpStatus, bizCode int)) *Kernel {
	if fn != nil {
		k.mapError = fn
	}
	return k
}

// SetContextDecorator 追加上下文装饰（在框架适配器注入原生上下文之后执行）。
// 约定：纯派生（轻量 WithValue）；重操作（鉴权、查库）请做拦截器——
// 每个请求（含校验失败的请求）都会执行装饰。
func (k *Kernel) SetContextDecorator(fn func(ctx context.Context, r RequestReader) context.Context) *Kernel {
	if fn != nil {
		prev := k.decorate
		k.decorate = func(ctx context.Context, r RequestReader) context.Context {
			if prev != nil {
				ctx = prev(ctx, r)
			}
			return fn(ctx, r)
		}
	}
	return k
}

// AddValidator 注册自定义校验器（绑定后执行，按注册顺序）。
// validator.Playground() 接入 go-playground 完整规则；不注册则零额外依赖。
func (k *Kernel) AddValidator(fn ValidatorFunc) *Kernel {
	if fn != nil {
		k.validators = append(k.validators, fn)
	}
	return k
}

// errHandled 管线内部哨兵：响应已写出，拦截链无需再处理。
var errHandled = errors.New("hinge: response already written")

// Handle 装配一个端点：拦截器在装配期解析（未注册的名字直接 panic，杜绝
// 静默失效），响应壳与状态码预计算；返回框架适配器逐请求调用的处理函数。
//
// bindQ / bindB 为生成的绑定器（无入参时传 nil）；h 为生成的闭包适配形态。
func (k *Kernel) Handle(ep Endpoint, bindQ, bindB Binder, h HandlerFunc) func(RequestReader, Sink) {
	env := k.envelopeFor(ep)
	success := ep.Status
	if success == 0 {
		success = http.StatusOK
	}
	// 拦截链顺序：Middleware → Limit → Auth（Auth 最贴近管线）
	names := make([]string, 0, len(ep.Middleware)+2)
	names = append(names, ep.Middleware...)
	if ep.Limit != "" {
		names = append(names, ep.Limit)
	}
	if ep.Auth != "" {
		names = append(names, ep.Auth)
	}
	chain := make([]Interceptor, 0, len(names))
	for _, n := range names {
		chain = append(chain, MustInterceptor(n))
	}
	timeout := ep.Timeout

	return func(r RequestReader, s Sink) {
		ctx := r.Context()
		if k.correlation {
			cid, _ := r.Header(HeaderCorrelationID)
			if cid == "" {
				cid = NewCorrelationID()
			}
			s.SetHeader(HeaderCorrelationID, cid)
			ctx = WithCorrelationID(ctx, cid)
		}
		if k.decorate != nil {
			ctx = k.decorate(ctx, r)
		}
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		run := func(ctx context.Context) error {
			k.serve(ctx, ep, r, s, env, success, bindQ, bindB, h)
			return nil // 响应已写出（成功或失败），拦截链不再处理
		}
		for i := len(chain) - 1; i >= 0; i-- {
			ic, next := chain[i], run
			run = func(ctx context.Context) error {
				return ic(ctx, ep, r, s, next)
			}
		}
		if err := run(ctx); err != nil {
			// 拦截器短路返回的错误：走统一错误链写出
			status, code, msg := ResolveError(k.mapError, err)
			k.writeFail(s, env, status, code, msg, err)
		}
	}
}

// envelopeFor 解析端点响应壳：命名壳未注册时 fail fast（与拦截器同策略，
// 杜绝静默回退默认壳导致的行为漂移）。
func (k *Kernel) envelopeFor(ep Endpoint) Envelope {
	if ep.Envelope == "" {
		return k.envelope
	}
	regMu.RLock()
	_, ok := envelopes[ep.Envelope]
	regMu.RUnlock()
	if !ok {
		panic("hinge: envelope not registered: " + ep.Envelope + "（RegisterEnvelope）")
	}
	return envelopes[ep.Envelope]
}
// serve 单请求管线：绑定 → 校验 → 调用 → 出参转换 → 状态码决策 → 壳包装 → 写出。
func (k *Kernel) serve(ctx context.Context, ep Endpoint, r RequestReader, s Sink, env Envelope, success int, bindQ, bindB Binder, h HandlerFunc) {
	var qv, bv any
	if bindQ != nil {
		v, err := bindQ(ctx, r)
		if err != nil {
			k.bindFail(s, env, err)
			return
		}
		qv = v
	}
	if bindB != nil {
		v, err := bindB(ctx, r)
		if err != nil {
			k.bindFail(s, env, err)
			return
		}
		bv = v
	}
	// Validate() 由生成的绑定器直调（生成期已知接收者形态，零反射）；
	// 手写逃生口的端点请在闭包内自行调用。此处不再兜底，避免与绑定器重复执行。
	for _, fn := range k.validators {
		if err := fn(ctx, ep, qv, bv); err != nil {
			k.bindFail(s, env, err)
			return
		}
	}

	out, err := h(ctx, qv, bv)
	if err != nil {
		status, code, msg := ResolveError(k.mapError, err)
		k.writeFail(s, env, status, code, msg, err)
		return
	}

	status := success
	if w, ok := out.(ResponseWrapper); ok {
		if w.ResponseStatus() != 0 {
			status = w.ResponseStatus()
		}
		for key, val := range w.ResponseHeaders() {
			s.SetHeader(key, val)
		}
		for _, ck := range w.ResponseCookies() {
			s.AddCookie(ck)
		}
		out = w.ResponseData()
	}

	out, err = TransformOut(ctx, out)
	if err != nil {
		status, code, msg := ResolveError(k.mapError, err)
		k.writeFail(s, env, status, code, msg, err)
		return
	}

	// FileStream 直接输出流；其余（含 Empty/any 占位、nil 数据）统一壳写出。
	switch fv := out.(type) {
	case *FileStream:
		if fv == nil {
			k.writeFail(s, env, http.StatusNotFound, http.StatusNotFound, "file not found", nil)
			return
		}
		s.WriteStream(fv)
	case FileStream:
		s.WriteStream(&fv)
	default:
		s.WriteJSON(status, env.Success(status, out))
	}
}

// bindFail 绑定/校验阶段错误（与 v0.1 bindError 语义逐条对齐）：
// *BindError（字段级明细）→ BindFail 状态 + FieldFailure 壳输出；
// 自带状态码的错误（StatusError/StatusCoder）优先兑现；
// 其余普通错误 → BindFail(k.bindStatus) + err.Error()。
func (k *Kernel) bindFail(s Sink, env Envelope, err error) {
	var be *BindError
	if errors.As(err, &be) {
		status, code := BindFail(k.bindStatus)
		k.writeFail(s, env, status, code, be.Error(), err)
		return
	}
	if status, code, msg, ok := ResolveErrorStatus(err); ok {
		k.writeFail(s, env, status, code, msg, err)
		return
	}
	status, code := BindFail(k.bindStatus)
	k.writeFail(s, env, status, code, err.Error(), err)
}

// writeFail 失败写出：错误链携带 AggregateError / BindError 且壳实现对应可选
// 接口时输出明细（aggregated_error / bind_errors）；其余情况输出统一壳。
func (k *Kernel) writeFail(s Sink, env Envelope, status, code int, msg string, err error) {
	var agg *AggregateError
	if errors.As(err, &agg) {
		if ae, ok := env.(AggregateEnvelope); ok {
			s.WriteJSON(status, ae.AggregateFailure(status, code, msg, agg.Failed))
			return
		}
	}
	var be *BindError
	if errors.As(err, &be) {
		if fe, ok := env.(FieldErrorEnvelope); ok {
			s.WriteJSON(status, fe.FieldFailure(status, code, msg, be.Fields))
			return
		}
	}
	s.WriteJSON(status, env.Failure(status, code, msg))
}

// isNilValue 判断 any 是否为 nil（含底层为 nil 的指针/接口等）。
// 仅用于跳过空 Q/B 的 Validate 兜底；主路径不经过这里。
func isNilValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	}
	return false
}
