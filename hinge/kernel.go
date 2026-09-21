package hinge

import (
	"context"
	"errors"
	"net/http"
	"reflect"
)

// Kernel 框架无关的请求管线内核。装配期（Handle）完成拦截器解析与
// 响应壳选择；请求期只做：装饰 ctx → 关联 ID → 拦截链 →
// 绑定（生成的 Binder）→ 校验 → 调用（生成的闭包）→ 出参转换 → 壳包装 → 写出。
// 全程除「值类型出参 + 指针接收者 OutTransform」拷贝外零反射。
//
// 错误策略：内核不解释错误——失败侧把原始 error 交给响应壳，
// 由壳决定 (HTTP 状态码, 响应体)；业务码语义属于业务层。
type Kernel struct {
	envelope Envelope
	// correlation 关联 ID 注入开关（默认关闭）。
	correlation bool
	// decorate 上下文装饰：请求期最前执行（Q/B 绑定之前），默认由框架适配器
	decorate func(ctx context.Context, r RequestReader) context.Context
	// validators 自定义校验器：绑定后按注册顺序执行（生成的绑定器已含
	// 必填检查与 Validate() 调用；这里只跑注入的自定义校验器）。
	validators []ValidatorFunc
}

// ValidatorFunc 自定义校验器签名。q/b 为解析后的请求值（可能为 nil）。
type ValidatorFunc func(ctx context.Context, ep Endpoint, q, b any) error

// NewKernel 创建内核
func NewKernel() *Kernel {
	return &Kernel{
		envelope: RawEnvelope{},
	}
}

// SetEnvelope 设置默认响应壳。路由级覆盖详见 Endpoint.Envelope
// （oapi:envelope 注解 + RegisterEnvelope 命名注册）。
func (k *Kernel) SetEnvelope(env Envelope) *Kernel {
	if env != nil {
		k.envelope = env
	}
	return k
}

// SetCorrelation 开启请求关联 ID（X-Correlation-Id）：入站沿用、缺失生成 UUIDv4，
// 注入请求 ctx 并回写响应头。默认关闭。
func (k *Kernel) SetCorrelation(enable bool) *Kernel {
	k.correlation = enable
	return k
}

// SetContextDecorator 追加上下文装饰（在框架适配器注入原生上下文之后执行）。
// 注意：每个请求（含校验失败的请求）都会执行装饰。
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
func (k *Kernel) AddValidator(fn ValidatorFunc) *Kernel {
	if fn != nil {
		k.validators = append(k.validators, fn)
	}
	return k
}

// errHandled 管线内部哨兵：响应已写出，拦截链无需再处理。
var errHandled = errors.New("hinge: response already written")

// Handle 装配一个端点
//
// bindQ / bindB 为生成的绑定器（无入参时传 nil）；h 为生成的闭包适配形态；
// extra 请求拦截器链。
func (k *Kernel) Handle(ep Endpoint, bindQ, bindB Binder, h HandlerFunc, extra ...Interceptor) func(RequestReader, Sink) {
	env := k.envelopeFor(ep)
	success := ep.Status
	if success == 0 {
		success = http.StatusOK
	}

	chain := extra
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
			// 拦截器短路返回的错误：交响应壳统一写出
			k.writeFail(s, env, err)
		}
	}
}

// envelopeFor 解析端点响应壳：命名壳未注册时 fail fast
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
// 失败路径不解释错误：原始 err 直通 env.Failure，由壳决定状态码与响应体。
func (k *Kernel) serve(ctx context.Context, ep Endpoint, r RequestReader, s Sink, env Envelope, success int, bindQ, bindB Binder, h HandlerFunc) {
	var qv, bv any
	if bindQ != nil {
		v, err := bindQ(ctx, r)
		if err != nil {
			k.writeFail(s, env, err)
			return
		}
		qv = v
	}
	if bindB != nil {
		v, err := bindB(ctx, r)
		if err != nil {
			k.writeFail(s, env, err)
			return
		}
		bv = v
	}

	for _, fn := range k.validators {
		if err := fn(ctx, ep, qv, bv); err != nil {
			k.writeFail(s, env, err)
			return
		}
	}

	out, err := h(ctx, qv, bv)
	if err != nil {
		k.writeFail(s, env, err)
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
		k.writeFail(s, env, err)
		return
	}

	// FileStream 直接输出流；其余（含 Empty/any 占位、nil 数据）统一壳写出。
	switch fv := out.(type) {
	case *FileStream:
		if fv == nil {
			k.writeFail(s, env, NotFound("file not found"))
			return
		}
		s.WriteStream(fv)
	case FileStream:
		s.WriteStream(&fv)
	default:
		s.WriteJSON(status, env.Success(status, out))
	}
}

// writeFail 失败写出：壳完全拥有 (HTTP 状态码, 响应体) 决策权，
// 绑定/校验、业务、转换、拦截器短路等所有失败路径共用此出口。
func (k *Kernel) writeFail(s Sink, env Envelope, err error) {
	status, body := env.Failure(err)
	s.WriteJSON(status, body)
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
