package contract

import (
	"errors"
	"fmt"
	"net/http"
)

// StatusCoder 最小契约：任何错误实现该接口即可携带 HTTP 状态码。
// 适配器错误路径按优先级识别：StatusError → StatusCoder → SetErrorMapper 全局映射
type StatusCoder interface {
	error
	StatusCode() int
}

// StatusError 携带状态码/业务码/对外信息的错误。
// 业务层直接返回：return nil, contract.NotFound("用户不存在")
//
// 字段规则：
//   - Status：HTTP 状态码；0 视为未设置，StatusCode() 兜底 500
//   - Code：业务码；0 视为未设置，适配器沿用默认约定
//     （HTTP 200 → 默认业务错误码；非 200 → 跟随状态码，与现有 ErrNotFound 映射一致）
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

// NotFound 404 资源不存在（替代直接使用 ErrNotFound 哨兵值，可携带信息）
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
