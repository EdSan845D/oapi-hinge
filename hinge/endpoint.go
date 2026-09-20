// Package hinge oapi-hinge v0.2 运行时内核：框架无关的请求管线与端点契约。
//
// v0.2 范式（RFC）：端点函数 + oapi:* 注解是唯一事实源；路由注册、类型化绑定器、
// 由 hinge gen 代码生成产出（见 gen/ 与 cmd/hinge）。运行时只消费生成产物,请求期零反射。
//
// 构造 Endpoint + Binder + HandlerFunc 调 Kernel.Handle，
// 即可把任意端点挂到任意框架适配器（servergin / serverecho / serverhttp）。
package hinge

import (
	"context"
	"reflect"
	"sync"
	"time"
)

// Endpoint 端点描述（运行时）
// 只承载请求管线需要的字段；文档元数据见 EndpointDoc
type Endpoint struct {
	// Owner 端点所属 Enterpoint 结构体名（拦截器/校验器日志与错误上下文用）。
	Owner string
	// Handler 端点方法名。
	Handler string
	Method  string // HTTP 方法
	Path    string // 完整路径（含组前缀），OpenAPI 风格 {id}
	// Status 成功状态码；0 → 200。
	Status int
	// Envelope 响应壳注册名（RegisterEnvelope）；空 → 内核默认壳。
	Envelope string
	// Timeout 端点超时；0 → 不限时。
	Timeout time.Duration
}

// EndpointDoc 文档侧端点描述：openapi.Generate 专用，运行时二进制不链接
type EndpointDoc struct {
	// Endpoint 运行时端点描述
	Endpoint
	// Summary / Description 文档信息（生成自函数注释）。
	Summary     string
	Description string
	Tags        []string
	// Deprecated 弃用标记（文档）。
	Deprecated bool
	// MWRefs 文档侧中间件引用名单
	// 收录框架原生中间件（oapi:middleware）与内核拦截器（oapi:interceptor）
	MWRefs []string
	// QType / BType / RType 类型信息
	QType, BType, RType reflect.Type
}

// HandlerFunc 统一处理函数的适配形态：生成闭包把强类型端点方法
// func(ctx, Q[, B]) (R, error) 包装成本形态（直接类型断言调用，请求期零反射）。
type HandlerFunc func(ctx context.Context, q, b any) (any, error)

// Binder 绑定函数适配形态：生成绑定器解析原始请求值为强类型入参
// （含 InTransform / Validate 调用与必填检查）。
// 绑定失败 *BindError 连同原始错误直通响应壳解释。
type Binder func(ctx context.Context, r RequestReader) (any, error)

// Type 返回 T 的 reflect.Type（生成表填充 EndpointDoc.QType 等使用，文档生成）。
func Type[T any]() reflect.Type {
	return reflect.TypeFor[T]()
}

// NoReq 无入参的端点使用该类型占位（生成器把 NoReq/any 视为无 Q）。
type NoReq = any

// Empty 无响应数据的操作使用该类型占位（序列化为 data: null）。
type Empty = any

// ---- 注册表 ----
var (
	regMu     sync.RWMutex
	envelopes = map[string]Envelope{}
)

// Interceptor 环绕拦截器：包装整条请求管线（绑定之前可短路）。
type Interceptor func(ctx context.Context, ep Endpoint, r RequestReader, s Sink, next func(context.Context) error) error

// RegisterEnvelope 注册命名响应壳（oapi:envelope <name> 引用）。
func RegisterEnvelope(name string, env Envelope) {
	regMu.Lock()
	defer regMu.Unlock()
	envelopes[name] = env
}

// EnvelopeFor 取命名响应壳；未注册回退 fallback。
func EnvelopeFor(name string, fallback Envelope) Envelope {
	if name == "" {
		return fallback
	}
	regMu.RLock()
	defer regMu.RUnlock()
	if env, ok := envelopes[name]; ok {
		return env
	}
	return fallback
}
