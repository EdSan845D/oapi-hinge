package response

import "strings"

// BindFieldError 单个字段的绑定/校验错误明细。
// Field 为字段定位（JSON 名 / form 名 / query 名等，由适配器尽力提供）；
// In 标记来源：body / query / form / path / header / cookie / validate；
// Msg 为对外可读信息。
type BindFieldError struct {
	Field string `json:"field"`
	In    string `json:"in"`
	Msg   string `json:"msg"`
}

// BindError 绑定/校验阶段的字段级错误：适配器与校验器在解析/校验失败时构造，
// 逐字段收集（不快速失败），Error() 输出汇总文本作为响应 msg。
// 经 errors.As 链识别后，由实现 FieldErrorEnvelope 的壳输出到 bind_errors 字段。
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

// FieldErrorEnvelope 可选扩展接口：支持字段级错误明细输出的壳。
// 适配器在错误链中发现 response.BindError 且壳实现该接口时调用 FieldFailure；
// 未实现的壳行为不变（仅输出汇总 msg）。
type FieldErrorEnvelope interface {
	Envelope
	// FieldFailure 失败响应包装（含字段级明细）。status/code/msg 语义同 Failure，
	// fields 为逐字段明细（绑定/校验阶段收集）。
	FieldFailure(status int, code int, msg string, fields []BindFieldError) any
}

// DefaultEnvelope 对字段级明细的原生支持：bind_errors 进入默认壳。
func (e DefaultEnvelope) FieldFailure(status int, code int, msg string, fields []BindFieldError) any {
	return Response[any]{Code: code, Data: nil, Msg: msg, BindErrors: fields}
}
