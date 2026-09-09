package hinge

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// ---- 内核错误决策矩阵：ResolveErrorStatus / ResolveError / BindFail ----

func TestResolveErrorStatus(t *testing.T) {
	// StatusError：Code 缺省时非 200 跟随状态码，200 用 CodeError
	status, code, msg, ok := ResolveErrorStatus(&StatusError{Status: http.StatusNotFound, Msg: "用户不存在"})
	if !ok || status != 404 || code != 404 || msg != "用户不存在" {
		t.Fatalf("StatusError 404: (%d, %d, %q, %v)", status, code, msg, ok)
	}
	status, code, _, ok = ResolveErrorStatus(&StatusError{Status: http.StatusOK})
	if !ok || status != 200 || code != CodeError {
		t.Fatalf("StatusError 200: (%d, %d, %v)", status, code, ok)
	}
	// 显式 Code 优先
	_, code, _, _ = ResolveErrorStatus(&StatusError{Status: http.StatusConflict, Code: 1001})
	if code != 1001 {
		t.Fatalf("explicit code = %d, want 1001", code)
	}
	// StatusCoder 自定义实现（非 StatusError）：code 用 CodeError，msg 用 err.Error()
	status, code, msg, ok = ResolveErrorStatus(customCoded{status: 429, msg: "too many"})
	if !ok || status != 429 || code != CodeError || msg != "too many" {
		t.Fatalf("StatusCoder: (%d, %d, %q, %v)", status, code, msg, ok)
	}
	// 普通错误 → ok=false（交给 errorMapper / bindStatus 兜底）
	if _, _, _, ok := ResolveErrorStatus(errors.New("plain")); ok {
		t.Fatal("plain error should not be resolved by status")
	}
	// Unwrap 链穿透：包装后仍可识别
	if _, _, _, ok := ResolveErrorStatus(wrapStatus()); !ok {
		t.Fatal("wrapped StatusError should be recognized")
	}
}

func wrapStatus() error {
	return wrap1(&StatusError{Status: http.StatusForbidden, Msg: "无权限"})
}

type customCoded struct {
	status int
	msg    string
}

func (c customCoded) Error() string   { return c.msg }
func (c customCoded) StatusCode() int { return c.status }

func TestResolveError(t *testing.T) {
	// StatusError 优先于 mapper
	status, code, msg := ResolveError(DefaultErrorMapper, NotFound("用户不存在"))
	if status != 404 || code != 404 || msg != "用户不存在" {
		t.Fatalf("NotFound via status: (%d, %d, %q)", status, code, msg)
	}
	// ErrNotFound 哨兵 → 404（DefaultErrorMapper）
	status, code, msg = ResolveError(nil, ErrNotFound)
	if status != 404 || code != 404 || msg != "not found" {
		t.Fatalf("ErrNotFound sentinel: (%d, %d, %q)", status, code, msg)
	}
	// 包装的 ErrNotFound 仍被 errors.Is 识别
	status, _, _ = ResolveError(nil, wrap1(ErrNotFound))
	if status != 404 {
		t.Fatalf("wrapped ErrNotFound status = %d", status)
	}
	// 普通业务错误 → HTTP 200 + code=7
	status, code, msg = ResolveError(nil, errors.New("业务失败"))
	if status != 200 || code != CodeError || msg != "业务失败" {
		t.Fatalf("plain biz error: (%d, %d, %q)", status, code, msg)
	}
	// 自定义 mapper
	status, code, _ = ResolveError(func(error) (int, int) { return 418, 42 }, errors.New("x"))
	if status != 418 || code != 42 {
		t.Fatalf("custom mapper: (%d, %d)", status, code)
	}
}

func TestBindFail(t *testing.T) {
	if s, c := BindFail(http.StatusOK); s != 200 || c != CodeError {
		t.Fatalf("BindFail(200) = (%d, %d)", s, c)
	}
	if s, c := BindFail(0); s != 200 || c != CodeError {
		t.Fatalf("BindFail(0) = (%d, %d)", s, c)
	}
	if s, c := BindFail(400); s != 400 || c != 400 {
		t.Fatalf("BindFail(400) = (%d, %d)", s, c)
	}
}

func TestStatusErrorMessageAndUnwrap(t *testing.T) {
	se := &StatusError{Status: 400, Msg: "对外信息", Err: errors.New("内部细节")}
	if got := se.Error(); got != "对外信息: 内部细节" {
		t.Fatalf("Error() = %q", got)
	}
	if unwrapped := se.Unwrap(); unwrapped == nil || unwrapped.Error() != "内部细节" {
		t.Fatalf("Unwrap = %v", unwrapped)
	}
	if got := (&StatusError{Status: 500}).Error(); got != "http status error (500)" {
		t.Fatalf("bare Error() = %q", got)
	}
	// WithCause：内部原因进入 Error() 链（日志可见），对外响应走 Msg 字段
	//（内核 ResolveErrorStatus：Msg 非空时响应只用 Msg，不泄露 cause）
	caused := WithCause(BadRequest("参数错误"), errors.New("secret detail"))
	if got := caused.Error(); !strings.Contains(got, "secret detail") {
		t.Fatalf("WithCause should keep cause in error chain: %q", got)
	}
	var se2 *StatusError
	if !errors.As(caused, &se2) || se2.Status != 400 || se2.Msg != "参数错误" {
		t.Fatalf("WithCause should unwrap to StatusError")
	}
	// 内核对外路径：Msg 非空时响应 msg 只用 Msg，不拼 cause
	status, code, msg, ok := ResolveErrorStatus(caused)
	if !ok || status != 400 || msg != "参数错误" || strings.Contains(msg, "secret") {
		t.Fatalf("response msg leaked cause: (%d, %q)", status, msg)
	}
	_ = code
}

// ---- 测试辅助：errors 包装链 ----

func wrap1(err error) error { return wrapErr{"w1", err} }

type wrapErr struct {
	msg string
	err error
}

func (w wrapErr) Error() string { return w.msg + ": " + w.err.Error() }
func (w wrapErr) Unwrap() error { return w.err }
