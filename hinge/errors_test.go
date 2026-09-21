package hinge

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// ---- InspectError：错误链一次性解析矩阵（壳实现者的便利工具） ----

func TestInspectError(t *testing.T) {
	// StatusError：自带状态/业务码/对外文案
	v := InspectError(&StatusError{Status: http.StatusNotFound, Msg: "用户不存在"})
	if v.Status != 404 || v.Code != 0 || v.Msg != "用户不存在" || v.Bind != nil || v.Aggregate != nil {
		t.Fatalf("StatusError inspect: %+v", v)
	}
	// 显式业务码原样透传（框架不做缺省填充）
	v = InspectError(&StatusError{Status: http.StatusConflict, Code: 1001})
	if v.Status != 409 || v.Code != 1001 {
		t.Fatalf("explicit code inspect: %+v", v)
	}
	// StatusError 零状态 → StatusCode() 兑底 500
	v = InspectError(&StatusError{Msg: "x"})
	if v.Status != 500 || v.Msg != "x" {
		t.Fatalf("zero status inspect: %+v", v)
	}
	// StatusCoder 自定义实现（非 StatusError）：code 不填充
	v = InspectError(customCoded{status: 429, msg: "too many"})
	if v.Status != 429 || v.Code != 0 || v.Msg != "too many" {
		t.Fatalf("StatusCoder inspect: %+v", v)
	}
	// StatusCoder 零状态 → 500
	v = InspectError(customCoded{msg: "zero"})
	if v.Status != 500 {
		t.Fatalf("StatusCoder zero status = %d, want 500", v.Status)
	}
	// 普通错误：只陈述事实（全零 + Msg）
	v = InspectError(errors.New("plain"))
	if v.Status != 0 || v.Code != 0 || v.Msg != "plain" {
		t.Fatalf("plain inspect: %+v", v)
	}
	// Unwrap 链穿透：包装后仍可识别
	v = InspectError(wrapStatus())
	if v.Status != 403 || v.Msg != "无权限" {
		t.Fatalf("wrapped StatusError inspect: %+v", v)
	}
	// 绑定错误：Bind 命中，状态码留给壳决策
	be := &BindError{}
	be.AddField("name", "body", "is required")
	v = InspectError(be)
	if v.Bind == nil || v.Status != 0 || v.Msg != "name: is required" {
		t.Fatalf("bind inspect: %+v", v)
	}
	// 聚合错误：Aggregate 命中，整体语义来自内嵌 StatusError
	agg := &AggregateError{StatusError: StatusError{Status: 200, Msg: "部分失败"}, Total: 3,
		Failed: []ItemError{{Key: "1"}}}
	v = InspectError(agg)
	if v.Aggregate == nil || v.Status != 200 || v.Msg != "部分失败" || v.Aggregate.Total != 3 {
		t.Fatalf("aggregate inspect: %+v", v)
	}
	// nil 安全
	if v := InspectError(nil); v.Msg != "" || v.Status != 0 {
		t.Fatalf("nil inspect: %+v", v)
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

// ---- StatusError 基础语义：Error() / Unwrap / WithCause / WithCode ----

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
	//（InspectError：Msg 非空时只用 Msg，不泄露 cause）
	caused := WithCause(BadRequest("参数错误"), errors.New("secret detail"))
	if got := caused.Error(); !strings.Contains(got, "secret detail") {
		t.Fatalf("WithCause should keep cause in error chain: %q", got)
	}
	var se2 *StatusError
	if !errors.As(caused, &se2) || se2.Status != 400 || se2.Msg != "参数错误" {
		t.Fatalf("WithCause should unwrap to StatusError")
	}
	// 对外路径：Msg 非空时响应 msg 只用 Msg，不拼 cause
	v := InspectError(caused)
	if v.Status != 400 || v.Msg != "参数错误" || strings.Contains(v.Msg, "secret") {
		t.Fatalf("response msg leaked cause: %+v", v)
	}
}

func TestWithCode(t *testing.T) {
	base := NotFound("用户不存在")
	// 克隆语义：原错误不被改写
	coded := WithCode(base, 40001)
	v := InspectError(coded)
	if v.Code != 40001 || v.Status != 404 {
		t.Fatalf("WithCode inspect: %+v", v)
	}
	if got := InspectError(base); got.Code != http.StatusNotFound {
		t.Fatalf("WithCode should not mutate source: %+v", got)
	}
	// 非状态错误原样返回
	plain := errors.New("plain")
	if WithCode(plain, 1) != plain {
		t.Fatal("WithCode should pass through non-status errors")
	}
}

func TestConvenienceConstructorsCarryCode(t *testing.T) {
	// 便捷构造器：业务码默认与状态码一致（构造期显式可见，运行期无隐式规则）
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"BadRequest", BadRequest("x"), 400},
		{"Unauthorized", Unauthorized("x"), 401},
		{"Forbidden", Forbidden("x"), 403},
		{"NotFound", NotFound("x"), 404},
		{"Conflict", Conflict("x"), 409},
		{"Internal", Internal("x"), 500},
		{"NewStatusError", NewStatusError(418, "x"), 418},
	}
	for _, c := range cases {
		v := InspectError(c.err)
		if v.Status != c.status || v.Code != c.status {
			t.Fatalf("%s = (%d, %d), want (%d, %d)", c.name, v.Status, v.Code, c.status, c.status)
		}
	}
}

// ---- 测试辅助：errors 包装链 ----

func wrap1(err error) error { return wrapErr{"w1", err} }

type wrapErr struct {
	msg string
	err error
}

func (w wrapErr) Error() string { return w.msg + ": " + w.err.Error() }
func (w wrapErr) Unwrap() error { return w.err }
