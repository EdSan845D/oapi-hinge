package hinge

import (
	"errors"
	"net/http"
	"strings"
)

// Envelope 响应壳接口：内核在写出前调用，统一同一服务的响应样式。
//
// 职责边界：
//   - 内核只递原材料：成功侧给 (status, data)，失败侧给原始 error。
//     成功状态码由端点声明决定（Endpoint.Status / Response[R].Status）。
//   - 壳完全拥有失败侧 (HTTP 状态码, 响应体) 的决策权：业务码、文案、
//     明细结构全部由壳自己解释。内核不做任何 错误→(code,msg) 预解析，
//     框架也不内置业务码常量——CodeOK/CodeError 之类属于业务层定义。
//   - 错误链可能携带 *StatusError / *BindError / *AggregateError，
//     壳用 InspectError 一次性取出，或直接 errors.As 自行解释。
//
// 自定义实现可输出任意风格（RESTful 裸输出、RFC 9457 Problem 等）；
// 文档侧壳 schema 由 openapi 生成器配对推导。
type Envelope interface {
	// Success 成功响应包装。status 为最终 HTTP 状态码（200/201/...），data 为业务数据。
	Success(status int, data any) any
	// Failure 失败响应包装：壳解释错误并返回 (HTTP 状态码, 响应体)。
	// err 为管线原始错误（绑定/校验、业务、出参转换、拦截器短路等）。
	Failure(err error) (status int, body any)
}

// Reply 统一响应壳的示例响应体（{code, data, msg}）。
// 明细字段由壳实现按错误链内容自行填充；需要别的响应体形态时
// 直接自定义 Envelope（以及自己的 body 结构）即可，内核不感知。
type Reply[T any] struct {
	Code int    `json:"code"`
	Data T      `json:"data"`
	Msg  string `json:"msg"`
	// AggregatedError 批量操作的部分失败明细（AggregateError.Failed），
	// omitempty 保证普通请求的响应体不变。
	AggregatedError any `json:"aggregated_error,omitempty"`
	// BindErrors 绑定/校验阶段的字段级错误明细（BindError.Fields），
	// omitempty 保证普通请求响应体不变。
	BindErrors []BindFieldError `json:"bind_errors,omitempty"`
}

// BizCodeEnvelope 业务码壳（内置示例实现，opt-in）：统一输出 {code, data, msg}。
// 业务码的取值与语义完全由使用者配置——框架不内置任何业务码常量：
//
//	k.SetEnvelope(hinge.BizCodeEnvelope{OKCode: 0, ErrCode: 10000, SuccessMsg: "ok"})
type BizCodeEnvelope struct {
	// OKCode 成功业务码（默认 0）。
	OKCode int
	// ErrCode 兜底业务码：错误未携带业务码（InspectError.Code == 0）时使用。
	// 0 视为未配置 → 用示例默认 7（与 OKCode 区分）。
	ErrCode int
	// SuccessMsg 成功文案；空则 "操作成功"。
	SuccessMsg string
	// PlainStatus 普通错误的 HTTP 状态码（错误未自带状态码时使用）。
	// 0 → 200（业务风格：HTTP 200 + 业务码区分成败）；REST 风格可设 500。
	// 错误自带状态码（StatusError / StatusCoder）时始终跟随错误。
	PlainStatus int
}

func (e BizCodeEnvelope) Success(_ int, data any) any {
	msg := e.SuccessMsg
	if msg == "" {
		msg = "操作成功"
	}
	return Reply[any]{Code: e.OKCode, Data: data, Msg: msg}
}

// Failure 失败输出：状态码跟随错误自带值，缺省用 PlainStatus（0 → 200）；
// 业务码取错误自带值，缺省用 ErrCode（未配置 → 7）；绑定/聚合明细进对应字段。
func (e BizCodeEnvelope) Failure(err error) (int, any) {
	v := InspectError(err)
	code := v.Code
	if code == 0 {
		code = e.ErrCode
		if code == 0 {
			code = 7
		}
	}
	status := v.Status
	if status == 0 {
		status = e.PlainStatus
		if status == 0 {
			status = http.StatusOK
		}
	}
	reply := Reply[any]{Code: code, Msg: v.Msg}
	if v.Bind != nil {
		reply.BindErrors = v.Bind.Fields
	}
	if v.Aggregate != nil {
		reply.AggregatedError = v.Aggregate.Failed
	}
	return status, reply
}

// RawEnvelope 裸响应壳（内核默认）：成功直接输出业务数据（RESTful 风格），
// 失败输出 {"error": msg}，状态码遵循 HTTP 语义：
// 错误自带状态码优先；ErrNotFound 哨兵 → 404；绑定/校验失败 → 400；其余 → 500。
type RawEnvelope struct{}

func (RawEnvelope) Success(_ int, data any) any { return data }

func (RawEnvelope) Failure(err error) (int, any) {
	v := InspectError(err)
	status := v.Status
	if status == 0 {
		switch {
		case errors.Is(err, ErrNotFound):
			status = http.StatusNotFound
		case v.Bind != nil:
			status = http.StatusBadRequest
		default:
			status = http.StatusInternalServerError
		}
	}
	return status, map[string]any{"error": v.Msg}
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
// 逐字段收集（不快速失败）。Error() 输出汇总文本，可作响应 msg。
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
