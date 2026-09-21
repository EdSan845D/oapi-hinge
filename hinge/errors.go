package hinge

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrNotFound 资源不存在哨兵（裸壳 RawEnvelope 将其映射为 HTTP 404；
// 业务码壳是否映射、映射成什么业务码由使用者决定，通常直接用 NotFound(msg)）。
var ErrNotFound = errors.New("not found")

// StatusCoder 最小契约：任何错误实现该接口即可携带 HTTP 状态码。
// 错误自带的状态码由响应壳解释（InspectError 提取）；框架不做全局映射。
type StatusCoder interface {
	error
	StatusCode() int
}

// StatusError 携带状态码/业务码/对外信息的错误。
// 业务层直接返回：return nil, hinge.NotFound("用户不存在")
//
// 字段规则：
//   - Status：HTTP 状态码；0 视为未设置，StatusCode() 兜底 500
//   - Code：业务码载体——取值与语义由业务层定义、由响应壳解释，
//     框架不做缺省填充；0 视为未设置（响应壳用自己配置的兜底码）
//   - Msg：对外错误信息；空则回退 err.Error()
//   - Err：内部错误（不对外暴露），支持 errors.Unwrap 链路穿透
type StatusError struct {
	Status int
	Code   int
	Msg    string
	Err    error
}

func (e *StatusError) Error() string {
	switch {
	case e.Msg != "" && e.Err != nil:
		return e.Msg + ": " + e.Err.Error()
	case e.Msg != "":
		return e.Msg
	case e.Err != nil:
		return e.Err.Error()
	default:
		return fmt.Sprintf("http status error (%d)", e.Status)
	}
}

// StatusCode 实现 StatusCoder；0 → 500
func (e *StatusError) StatusCode() int {
	if e.Status == 0 {
		return http.StatusInternalServerError
	}
	return e.Status
}

// Unwrap 支持错误链穿透（fmt.Errorf("...: %w", err) 后仍可被 errors.As 识别）
func (e *StatusError) Unwrap() error { return e.Err }

// NewStatusError 构造携带自定义状态码的错误（业务码默认与状态码一致）。
// 需要自定义业务码时用 WithCode 或直构 &StatusError{}。
func NewStatusError(status int, msg string) error {
	return &StatusError{Status: status, Code: status, Msg: msg}
}

// BadRequest 400 请求错误
func BadRequest(msg string) error {
	return &StatusError{Status: http.StatusBadRequest, Code: http.StatusBadRequest, Msg: msg}
}

// Unauthorized 401 未认证
func Unauthorized(msg string) error {
	return &StatusError{Status: http.StatusUnauthorized, Code: http.StatusUnauthorized, Msg: msg}
}

// Forbidden 403 无权限
func Forbidden(msg string) error {
	return &StatusError{Status: http.StatusForbidden, Code: http.StatusForbidden, Msg: msg}
}

// NotFound 404 资源不存在（可携带信息）
func NotFound(msg string) error {
	return &StatusError{Status: http.StatusNotFound, Code: http.StatusNotFound, Msg: msg}
}

// Conflict 409 冲突
func Conflict(msg string) error {
	return &StatusError{Status: http.StatusConflict, Code: http.StatusConflict, Msg: msg}
}

// Internal 500 内部错误（对外只暴露 msg，内部细节用 WithCause 附加）
func Internal(msg string) error {
	return &StatusError{Status: http.StatusInternalServerError, Code: http.StatusInternalServerError, Msg: msg}
}

// WithCode 给状态错误设置业务码（业务码语义由业务层定义、响应壳解释）。
// 返回克隆副本，不改写原错误；非状态错误原样返回。
func WithCode(err error, code int) error {
	if se, ok := errors.AsType[*StatusError](err); ok {
		clone := *se
		clone.Code = code
		return &clone
	}
	return err
}

// WithCause 给状态错误附加内部原因（err 只进日志/错误链，不对外）
func WithCause(statusErr error, cause error) error {
	if se, ok := errors.AsType[*StatusError](statusErr); ok {
		return &StatusError{Status: se.Status, Code: se.Code, Msg: se.Msg, Err: cause}
	}
	return statusErr
}

// ItemError 批量操作中的单项失败明细。
// Key 由业务层决定语义（批次索引、业务 ID 等），用于客户端定位失败项；
// Code/Msg 同样由业务层定义、响应壳原样输出。
type ItemError struct {
	Key  string `json:"key"`
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// AggregateError 「整体受理、部分失败」的批量错误。
// 内嵌 StatusError 描述整体语义（建议 Status 显式设置，如 200 受理；
// Msg 为整体文案）；逐项失败明细 Failed 由响应壳读取输出
// （InspectError().Aggregate / errors.As）。
type AggregateError struct {
	StatusError
	Total  int         // 批量总数
	Failed []ItemError // 失败明细
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

// ErrorView 错误链的一次性解析结果：响应壳实现者的便利工具。
// 只陈述错误自带的事实，不做任何缺省填充（零值 = 未携带）；
// 壳据此自行决定 (HTTP 状态码, 业务码, 文案) 与明细输出。
type ErrorView struct {
	// Status 错误自带的 HTTP 状态码（StatusError / StatusCoder）；未携带 → 0。
	Status int
	// Code 错误自带的业务码（仅 *StatusError.Code，原样透传）；未携带 → 0。
	Code int
	// Msg 对外文案：StatusError.Msg 优先，否则 err.Error()（不拼接内部 cause）。
	Msg string
	// Bind 字段级绑定/校验错误（errors.As 命中时非 nil）。
	Bind *BindError
	// Aggregate 批量部分失败（errors.As 命中时非 nil）。
	Aggregate *AggregateError
}

// InspectError 一次性解析错误链，供壳实现按需取用。
// 解析顺序：*AggregateError → *StatusError → *BindError → StatusCoder → 普通错误。
// 壳可以完全不用本函数、自行 errors.As——它只是便利封装，不是内核强制路径。
func InspectError(err error) ErrorView {
	if err == nil {
		return ErrorView{}
	}
	var v ErrorView
	if agg, ok := errors.AsType[*AggregateError](err); ok {
		v.Aggregate = agg
		v.Status = agg.StatusError.StatusCode()
		v.Code = agg.StatusError.Code
		if agg.StatusError.Msg != "" {
			v.Msg = agg.StatusError.Msg
		} else {
			v.Msg = err.Error()
		}
		return v
	}
	if se, ok := errors.AsType[*StatusError](err); ok {
		v.Status = se.StatusCode()
		v.Code = se.Code
		if se.Msg != "" {
			v.Msg = se.Msg
		} else {
			v.Msg = err.Error()
		}
		return v
	}
	if be, ok := errors.AsType[*BindError](err); ok {
		v.Bind = be
		v.Msg = err.Error()
		return v
	}
	if sc, ok := errors.AsType[StatusCoder](err); ok {
		v.Status = sc.StatusCode()
		if v.Status == 0 {
			v.Status = http.StatusInternalServerError
		}
		v.Msg = err.Error()
		return v
	}
	v.Msg = err.Error()
	return v
}

// IsBodyMethod 是否携带请求体的方法（生成器与手写挂载共用）。
func IsBodyMethod(method string) bool {
	return method == "POST" || method == "PUT" || method == "PATCH"
}
