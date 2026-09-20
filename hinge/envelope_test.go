package hinge

import (
	"errors"
	"net/http"
	"testing"
)

// ---- 响应壳：BizCode / Raw / 注册表 ----

func TestBizCodeEnvelope(t *testing.T) {
	env := BizCodeEnvelope{}
	out := env.Success(201, map[string]string{"id": "u1"})
	reply, ok := out.(Reply[any])
	if !ok {
		t.Fatalf("Success type = %T", out)
	}
	if reply.Code != 0 || reply.Msg != "操作成功" {
		t.Fatalf("Success reply = %+v", reply)
	}
	if data, ok := reply.Data.(map[string]string); !ok || data["id"] != "u1" {
		t.Fatalf("Success data mismatch: %+v", reply.Data)
	}

	// 普通业务错误：HTTP 200 + 兑底业务码（默认 7）
	status, body := env.Failure(errors.New("业务失败"))
	if status != 200 {
		t.Fatalf("plain error status = %d, want 200", status)
	}
	if fail := body.(Reply[any]); fail.Code != 7 || fail.Data != nil || fail.Msg != "业务失败" {
		t.Fatalf("plain failure reply = %+v", fail)
	}

	// 错误自带状态码：跟随；业务码未携带 → 兑底码
	status, body = env.Failure(NotFound("用户不存在"))
	fail := body.(Reply[any])
	if status != 404 || fail.Code != 404 || fail.Msg != "用户不存在" {
		t.Fatalf("NotFound failure = (%d, %+v)", status, fail)
	}

	// 错误自带业务码：优先于兑底码
	status, body = env.Failure(WithCode(Conflict("冲突"), 1001))
	if status != 409 || body.(Reply[any]).Code != 1001 {
		t.Fatalf("explicit code = (%d, %+v)", status, body)
	}

	// 业务码完全由使用者配置（框架无内置常量）
	custom := BizCodeEnvelope{OKCode: 0, ErrCode: 10000, SuccessMsg: "ok", PlainStatus: 500}
	status, body = custom.Failure(errors.New("boom"))
	if status != 500 || body.(Reply[any]).Code != 10000 {
		t.Fatalf("custom config = (%d, %+v)", status, body)
	}

	// 绑定错误：明细进 bind_errors（HTTP 200 + code=7 兑底）
	be := &BindError{}
	be.AddField("name", "body", "is required")
	status, body = env.Failure(be)
	fail = body.(Reply[any])
	if status != 200 || fail.Code != 7 || len(fail.BindErrors) != 1 || fail.BindErrors[0].Field != "name" {
		t.Fatalf("bind failure = (%d, %+v)", status, fail)
	}

	// 聚合错误：明细进 aggregated_error
	agg := &AggregateError{StatusError: StatusError{Status: http.StatusOK, Msg: "部分失败"}, Total: 2,
		Failed: []ItemError{{Key: "0", Msg: "x"}}}
	status, body = env.Failure(agg)
	fail = body.(Reply[any])
	if status != 200 || fail.Msg != "部分失败" || fail.AggregatedError == nil {
		t.Fatalf("aggregate failure = (%d, %+v)", status, fail)
	}
}

func TestRawEnvelope(t *testing.T) {
	env := RawEnvelope{}
	data := map[string]string{"id": "u1"}
	got, ok := env.Success(200, data).(map[string]string)
	if !ok || got["id"] != "u1" {
		t.Fatalf("Raw Success should pass data through: %v", got)
	}
	if out := env.Success(200, nil); out != nil {
		t.Fatalf("Raw Success(nil) = %v", out)
	}

	// 错误自带状态码：跟随
	status, body := env.Failure(NotFound("用户不存在"))
	if status != 404 || body.(map[string]any)["error"] != "用户不存在" {
		t.Fatalf("Raw Failure(StatusError) = (%d, %v)", status, body)
	}
	// ErrNotFound 哨兵 → 404
	status, _ = env.Failure(ErrNotFound)
	if status != 404 {
		t.Fatalf("Raw Failure(ErrNotFound) status = %d, want 404", status)
	}
	// 绑定错误 → 400
	status, body = env.Failure(&BindError{Fields: []BindFieldError{{Field: "name", Msg: "is required"}}})
	if status != 400 || body.(map[string]any)["error"] != "name: is required" {
		t.Fatalf("Raw Failure(BindError) = (%d, %v)", status, body)
	}
	// 普通错误 → 500（REST 语义）
	status, body = env.Failure(errors.New("boom"))
	if status != 500 || body.(map[string]any)["error"] != "boom" {
		t.Fatalf("Raw Failure(plain) = (%d, %v)", status, body)
	}
}

// RegisterEnvelope 同名注册 = 覆盖（允许程序化配置重复装配；
// 注意与 RegisterInterceptor 的 panic 语义不同，见汇报）
func TestRegisterEnvelopeOverwrite(t *testing.T) {
	RegisterEnvelope("envelope-overwrite-test", RawEnvelope{})
	if _, ok := EnvelopeFor("envelope-overwrite-test", nil).(RawEnvelope); !ok {
		t.Fatal("first registration should hit")
	}
	RegisterEnvelope("envelope-overwrite-test", BizCodeEnvelope{})
	if _, ok := EnvelopeFor("envelope-overwrite-test", nil).(BizCodeEnvelope); !ok {
		t.Fatal("re-registration should overwrite")
	}
}

func TestEnvelopeFor(t *testing.T) {
	RegisterEnvelope("envelope-for-test", BizCodeEnvelope{})
	if _, ok := EnvelopeFor("envelope-for-test", nil).(BizCodeEnvelope); !ok {
		t.Fatal("named envelope should hit registry")
	}
	got := EnvelopeFor("envelope-for-missing", RawEnvelope{})
	if _, ok := got.(RawEnvelope); !ok {
		t.Fatalf("missing name should fall back: %T", got)
	}
}

// ---- InspectError：StatusCoder 自定义实现 ----

func TestInspectErrorStatusCoder(t *testing.T) {
	var err error = customCoded{status: 429, msg: "too many"}
	v := InspectError(err)
	if v.Status != 429 || v.Code != 0 || v.Msg != "too many" || v.Bind != nil || v.Aggregate != nil {
		t.Fatalf("StatusCoder inspect: %+v", v)
	}
	// errors.As 识别 StatusCoder 接口
	var sc StatusCoder
	if !errors.As(err, &sc) || sc.StatusCode() != 429 {
		t.Fatal("errors.As should find StatusCoder")
	}
}
