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
