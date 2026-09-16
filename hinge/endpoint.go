// Package hinge oapi-hinge v0.2 运行时内核：框架无关的请求管线与端点契约。
//
// v0.2 范式（RFC）：端点函数 + oapi:* 注解是唯一事实源；路由注册、类型化绑定器、
// Endpoints() 表由 hinge gen 代码生成产出（见 gen/ 与 cmd/hinge）。运行时只消费
// 生成产物：Endpoint 描述 + Binder + 闭包适配的 HandlerFunc，请求期零反射。
//
// 手写逃生口：直接构造 Endpoint + Binder + HandlerFunc 调 Kernel.Handle，
// 即可把任意端点挂到任意框架适配器（servergin / serverecho / serverhttp）。
package hinge

import (
	"context"
	"reflect"
	"sync"
	"time"
)

// Endpoint 端点描述（运行时）：生成器产出的唯一事实，同时是手写逃生口的注册单元。
// 只承载请求管线需要的字段；文档元数据见 EndpointDoc —— 仅 openapi 开发期入口
// （go run -tags openapi）与生成器 docs_gen.go 消费，运行时二进制不链接，
// 文档字符串与类型描述零运行时开销。
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
// （生成器发到 docs_gen.go，仅被 openapi build-tag 入口引用，链接器剥离）。
// 运行时字段直接内嵌 Endpoint；生成器发射 docs_gen.go 时引用 specs_gen.go
// 的 SpecXxx 变量整段赋值 —— 运行时字段单一事实源，不重复声明；
// Summary / Description 等文档字段仅此存在。
type EndpointDoc struct {
	// Endpoint 运行时端点描述：与 Endpoints() 表同源（Owner/Handler/Method/
	// Path/Status/Envelope/Timeout）。生成器以 Endpoint: SpecXxx 整段赋值；
	// 手写字面量也可用提升字段名键（EndpointDoc{Method: ...}）。
	Endpoint

	// ---- 文档字段 ----
	// Summary / Description 文档信息（生成自函数注释）。
	Summary     string
	Description string
	Tags        []string
	// Deprecated 弃用标记（文档）。
	Deprecated bool
	// MWRefs 文档侧中间件引用名单（hinge gen 生成，全限定形态）：框架原生
	// 中间件（oapi:middleware）与内核拦截器（oapi:interceptor）两类都进，
	// 声明序 EntryPointConfig → 结构体级 → 方法级。openapi 生成器据此做
	// security scheme 推导（尾段名命中）与 RegisterMiddlewareDoc 钩子配对。
	MWRefs []string
	// QType / BType / RType 类型信息：openapi schema 生成消费。
	QType, BType, RType reflect.Type
}

// Enterpoint 端点集合接口：生成器为每个 Enterpoint 结构体产出
// Enterpoint() 标记与 Endpoints() 路径↔函数对应关系表，以及本接口的实现守卫。
type Enterpoint interface {
	Enterpoint()
	Endpoints() []Endpoint
}

// HandlerFunc 统一处理函数的适配形态：生成闭包把强类型端点方法
// func(ctx, Q[, B]) (R, error) 包装成本形态（直接类型断言调用，请求期零反射）。
type HandlerFunc func(ctx context.Context, q, b any) (any, error)

// Binder 绑定函数适配形态：生成绑定器解析原始请求值为强类型入参
// （含 InTransform / Validate 调用与必填检查）。返回 *BindError 走字段级
// 绑定失败链；其他 error 走统一错误决策（StatusError 优先，BindFail 兜底）。
type Binder func(ctx context.Context, r RequestReader) (any, error)

// Type 返回 T 的 reflect.Type（生成表填充 EndpointDoc.QType 等使用，文档生成）。
func Type[T any]() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

// NoReq 无入参的端点使用该类型占位（生成器把 NoReq/any 视为无 Q）。
type NoReq = any

// Empty 无响应数据的操作使用该类型占位（序列化为 data: null）。
type Empty = any

// MustDuration 解析 oapi:timeout 注解值（生成表填充 Endpoint.Timeout 使用）；
// 非法格式 panic（生成期已校验，运行时不会触发）。
func MustDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic("hinge: invalid duration " + s)
	}
	return d
}

// ---- 注册表：拦截器（auth / limit / middleware 共用）与命名响应壳 ----

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
