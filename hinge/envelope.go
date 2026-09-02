package hinge

import "strings"

// Envelope 响应壳接口：内核在写出前调用。成功与失败统一走该接口，
// 保证同一服务内响应格式一致；自定义实现可输出任意风格（RESTful 裸输出、RFC 9457 等）。
// 文档侧壳 schema 由 openapi 生成器配对推导。
type Envelope interface {
	// Success 成功响应包装。status 为最终 HTTP 状态码（200/201/...），data 为业务数据。
	Success(status int, data any) any
	// Failure 失败响应包装。status 为 HTTP 状态码，code 为业务码，msg 为错误信息。
	Failure(status int, code int, msg string) any
}

// Reply 统一响应壳（默认业务接口返回 {code, data, msg}）。
// 通过 Kernel.SetEnvelope 可替换为任意自定义壳。
type Reply[T any] struct {
	Code int    `json:"code"`
	Data T      `json:"data"`
	Msg  string `json:"msg"`
	// AggregatedError 批量操作的部分失败明细（AggregateError.Failed）。
	// 仅当错误链携带 AggregateError 且壳支持聚合输出时非空；
	// omitempty 保证普通请求的响应体不变。
	AggregatedError any `json:"aggregated_error,omitempty"`
	// BindErrors 绑定/校验阶段的字段级错误明细。仅当错误链携带 *BindError 且
	// 壳实现 FieldErrorEnvelope 时非空；omitempty 保证普通请求响应体不变。
	BindErrors []BindFieldError `json:"bind_errors,omitempty"`
}

// Paged 分页响应
type Paged[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
}

const (
	CodeOK    = 0 // 成功
	CodeError = 7 // 业务错误（默认）
)

// DefaultEnvelope 默认响应壳：{code, data, msg}。
// SuccessMsg 自定义成功文案；空则使用默认 "操作成功"。
type DefaultEnvelope struct {
	SuccessMsg string
}

func (e DefaultEnvelope) Success(_ int, data any) any {
	msg := e.SuccessMsg
	if msg == "" {
		msg = "操作成功"
	}
	return Reply[any]{Code: CodeOK, Data: data, Msg: msg}
}

func (DefaultEnvelope) Failure(status int, code int, msg string) any {
	return Reply[any]{Code: code, Data: nil, Msg: msg}
}

// RawEnvelope 裸响应壳：成功直接输出业务数据（RESTful 风格），
// 失败输出 {"error": msg}（与 HTTP 语义一致，不做业务码包装）。
type RawEnvelope struct{}

func (RawEnvelope) Success(_ int, data any) any { return data }

func (RawEnvelope) Failure(_ int, _ int, msg string) any {
	return map[string]any{"error": msg}
}

// AggregateEnvelope 可选扩展接口：支持批量部分失败聚合输出的壳。
// 内核在错误链中发现 *AggregateError 且壳实现该接口时调用 AggregateFailure
// （输出 aggregated_error 明细）；未实现的壳行为不变。
type AggregateEnvelope interface {
	Envelope
	AggregateFailure(status int, code int, msg string, agg any) any
}

// DefaultEnvelope 对聚合的原生支持：aggregated_error 明细进入默认壳。
func (e DefaultEnvelope) AggregateFailure(status int, code int, msg string, agg any) any {
	return Reply[any]{Code: code, Data: nil, Msg: msg, AggregatedError: agg}
}

// BindFieldError 单个字段的绑定/校验错误明细。
// Field 为字段定位（JSON 名 / form 名 / query 名等，由生成绑定器提供）；
// In 标记来源：body / query / form / path / header / cookie；
// Msg 为对外可读信息。
type BindFieldError struct {
	Field string `json:"field"`
	In    string `json:"in"`
	Msg   string `json:"msg"`
}

// BindError 绑定/校验阶段的字段级错误：生成绑定器在解析/必填失败时构造，
// 逐字段收集（不快速失败）。Error() 输出汇总文本作为响应 msg。
type BindError struct {
	Fields []BindFieldError
}

func (e *BindError) Error() string {
	if len(e.Fields) == 0 {
		return "bind failed"
	}
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		if f.Field != "" {
			parts = append(parts, f.Field+": "+f.Msg)
			continue
		}
		parts = append(parts, f.Msg)
	}
	return strings.Join(parts, "; ")
}

// AddField 追加一条字段错误（生成绑定器使用）。
func (e *BindError) AddField(field, in, msg string) {
	e.Fields = append(e.Fields, BindFieldError{Field: field, In: in, Msg: msg})
}

// FieldErrorEnvelope 可选扩展接口：支持字段级错误明细输出的壳。
// 内核在错误链中发现 *BindError 且壳实现该接口时调用 FieldFailure；
// 未实现的壳行为不变（仅输出汇总 msg）。
type FieldErrorEnvelope interface {
	Envelope
	FieldFailure(status int, code int, msg string, fields []BindFieldError) any
}

// DefaultEnvelope 对字段级明细的原生支持：bind_errors 进入默认壳。
func (e DefaultEnvelope) FieldFailure(status int, code int, msg string, fields []BindFieldError) any {
	return Reply[any]{Code: code, Data: nil, Msg: msg, BindErrors: fields}
}
