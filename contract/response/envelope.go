package response

// Envelope 响应壳接口：运行时适配器在写出前调用。
// 成功与失败统一走该接口，保证同一服务内响应格式一致；
// 自定义实现可输出任意风格（RESTful 裸输出、RFC 9457 等）。
// 文档生成器侧的壳 schema 由 openapi.OptionWithEnvelopeSchema 配对配置。
type Envelope interface {
	// Success 成功响应包装。status 为最终 HTTP 状态码（200/201/...），data 为业务数据。
	Success(status int, data any) any
	// Failure 失败响应包装。status 为 HTTP 状态码，code 为业务码，msg 为错误信息。
	Failure(status int, code int, msg string) any
}

// DefaultEnvelope 默认响应壳：{code, data, msg}。
// SuccessMsg 自定义成功文案；空则使用默认 "操作成功"。
type DefaultEnvelope struct {
	SuccessMsg string
}

func (e DefaultEnvelope) Success(status int, data any) any {
	msg := e.SuccessMsg
	if msg == "" {
		msg = "操作成功"
	}
	return Response[any]{Code: CodeOK, Data: data, Msg: msg}
}

func (DefaultEnvelope) Failure(status int, code int, msg string) any {
	return Response[any]{Code: code, Data: nil, Msg: msg}
}

// RawEnvelope 裸响应壳：成功直接输出业务数据（RESTful 风格），
// 失败输出 {"error": msg}（与 HTTP 语义一致，不做业务码包装）。
type RawEnvelope struct{}

func (RawEnvelope) Success(status int, data any) any { return data }

func (RawEnvelope) Failure(status int, code int, msg string) any {
	return map[string]any{"error": msg}
}

// AggregateEnvelope 可选扩展接口：支持批量部分失败聚合输出的壳。
// 适配器在错误链中发现 contract.AggregateError 且壳实现该接口时调用
// AggregateFailure（输出 aggregated_error 明细）；未实现的壳行为不变。
type AggregateEnvelope interface {
	Envelope
	// AggregateFailure 失败响应包装（含聚合明细）。status/code/msg 语义同 Failure，
	// agg 为逐项失败明细（contract.ItemError 切片）。
	AggregateFailure(status int, code int, msg string, agg any) any
}

// DefaultEnvelope 对聚合的原生支持：aggregated_error 明细进入默认壳。
func (e DefaultEnvelope) AggregateFailure(status int, code int, msg string, agg any) any {
	return Response[any]{Code: code, Data: nil, Msg: msg, AggregatedError: agg}
}
