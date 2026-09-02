package hinge

import (
	"errors"
	"fmt"
	"net/http"
)

// ErrNotFound 资源不存在（运行时映射为 HTTP 404）。
// 需要携带对外信息时优先使用 NotFound(msg)（StatusError）。
var ErrNotFound = errors.New("not found")

// StatusCoder 最小契约：任何错误实现该接口即可携带 HTTP 状态码。
// 错误决策优先级：StatusError → StatusCoder → SetErrorMapper 全局映射。
type StatusCoder interface {
	error
	StatusCode() int
}

// StatusError 携带状态码/业务码/对外信息的错误。
// 业务层直接返回：return nil, hinge.NotFound("用户不存在")
//
// 字段规则：
//   - Status：HTTP 状态码；0 视为未设置，StatusCode() 兜底 500
//   - Code：业务码；0 视为未设置，内核沿用默认约定
//     （HTTP 200 → 默认业务错误码；非 200 → 跟随状态码）
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

// NewStatusError 构造携带自定义状态码的错误
func NewStatusError(status int, msg string) error {
	return &StatusError{Status: status, Msg: msg}
}

// BadRequest 400 请求错误
func BadRequest(msg string) error { return &StatusError{Status: http.StatusBadRequest, Msg: msg} }

// Unauthorized 401 未认证
func Unauthorized(msg string) error { return &StatusError{Status: http.StatusUnauthorized, Msg: msg} }

// Forbidden 403 无权限
func Forbidden(msg string) error { return &StatusError{Status: http.StatusForbidden, Msg: msg} }

// NotFound 404 资源不存在（可携带信息）
func NotFound(msg string) error { return &StatusError{Status: http.StatusNotFound, Msg: msg} }

// Conflict 409 冲突
func Conflict(msg string) error { return &StatusError{Status: http.StatusConflict, Msg: msg} }

// Internal 500 内部错误（对外只暴露 msg，内部细节用 WithCause 附加）
func Internal(msg string) error {
	return &StatusError{Status: http.StatusInternalServerError, Msg: msg}
}

// WithCause 给状态错误附加内部原因（err 只进日志/错误链，不对外）
func WithCause(statusErr error, cause error) error {
	if se, ok := errors.AsType[*StatusError](statusErr); ok {
		return &StatusError{Status: se.Status, Code: se.Code, Msg: se.Msg, Err: cause}
	}
	return statusErr
}

// ItemError 批量操作中的单项失败明细。
// Key 由业务层决定语义（批次索引、业务 ID 等），用于客户端定位失败项。
type ItemError struct {
	Key  string `json:"key"`
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// AggregateError 「整体受理、部分失败」的批量错误。
// 整体语义走 StatusError 常规错误决策；逐项失败明细由实现
// AggregateEnvelope 的响应壳输出到 aggregated_error 字段。
type AggregateError struct {
	StatusError
	Total  int         // 批量总数
	Failed []ItemError // 失败明细
}

// ResolveErrorStatus 提取错误自带的状态信息（StatusError → StatusCoder）。
// ok=false 表示普通错误，由调用方决定兜底策略（业务错误走 errorMapper，
// 绑定错误走 bindStatus）。
func ResolveErrorStatus(err error) (status, code int, msg string, ok bool) {
	if se, e := errors.AsType[*StatusError](err); e {
		status = se.StatusCode()
		code = se.Code
		if code == 0 {
			if status == http.StatusOK {
				code = CodeError
			} else {
				code = status
			}
		}
		msg = se.Msg
		if msg == "" {
			msg = err.Error()
		}
		return status, code, msg, true
	}
	if sc, e := errors.AsType[StatusCoder](err); e {
		status = sc.StatusCode()
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return status, CodeError, err.Error(), true
	}
	return 0, 0, "", false
}

// ResolveError 业务错误解析：错误自带状态码优先，否则调用 mapError 兜底
// （mapError 为 nil 时用 DefaultErrorMapper）。
func ResolveError(mapError func(err error) (httpStatus, bizCode int), err error) (int, int, string) {
	if status, code, msg, ok := ResolveErrorStatus(err); ok {
		return status, code, msg
	}
	if mapError == nil {
		mapError = DefaultErrorMapper
	}
	status, code := mapError(err)
	return status, code, err.Error()
}

// DefaultErrorMapper 默认兜底映射：ErrNotFound → 404；其余业务错误 → HTTP 200 + code=7。
func DefaultErrorMapper(err error) (httpStatus, bizCode int) {
	if errors.Is(err, ErrNotFound) {
		return http.StatusNotFound, http.StatusNotFound
	}
	return http.StatusOK, CodeError
}

// BindFail 绑定/校验失败响应的 (status, code)：默认 200 + CodeError；
// 自定义非 200 状态码时 code 跟随状态码（与 StatusError 约定一致）。
func BindFail(status int) (int, int) {
	if status <= 0 || status == http.StatusOK {
		return http.StatusOK, CodeError
	}
	return status, status
}

// IsBodyMethod 是否携带请求体的方法（生成器与手写挂载共用）。
func IsBodyMethod(method string) bool {
	return method == "POST" || method == "PUT" || method == "PATCH"
}
