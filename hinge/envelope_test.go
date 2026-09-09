package hinge

import (
	"context"
	"errors"
	"testing"
)

// ---- 响应壳：Default / Raw / 扩展接口 / 注册表 ----

func TestDefaultEnvelope(t *testing.T) {
	env := DefaultEnvelope{}
	out := env.Success(201, map[string]string{"id": "u1"})
	reply, ok := out.(Reply[any])
	if !ok {
		t.Fatalf("Success type = %T", out)
	}
	if reply.Code != CodeOK || reply.Msg != "操作成功" {
		t.Fatalf("Success reply = %+v", reply)
	}
	if data, ok := reply.Data.(map[string]string); !ok || data["id"] != "u1" {
		t.Fatalf("Success data mismatch: %+v", reply.Data)
	}
	fail := env.Failure(404, 404, "用户不存在").(Reply[any])
	if fail.Code != 404 || fail.Data != nil || fail.Msg != "用户不存在" {
		t.Fatalf("Failure reply = %+v", fail)
	}
	// 扩展接口：聚合 / 字段明细
	agg := env.AggregateFailure(200, CodeError, "部分失败", []ItemError{{Key: "0"}}).(Reply[any])
	if agg.AggregatedError == nil {
		t.Fatal("AggregateFailure should carry aggregated_error")
	}
	field := env.FieldFailure(200, CodeError, "绑定失败", []BindFieldError{{Field: "name"}}).(Reply[any])
	if len(field.BindErrors) != 1 || field.BindErrors[0].Field != "name" {
		t.Fatalf("FieldFailure mismatch: %+v", field)
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
	fail := env.Failure(404, 404, "用户不存在").(map[string]any)
	if fail["error"] != "用户不存在" {
		t.Fatalf("Raw Failure = %v", fail)
	}
}

// RegisterEnvelope 同名注册 = 覆盖（允许程序化配置重复装配；
// 注意与 RegisterInterceptor 的 panic 语义不同，见汇报）
func TestRegisterEnvelopeOverwrite(t *testing.T) {
	RegisterEnvelope("envelope-overwrite-test", RawEnvelope{})
	if _, ok := EnvelopeFor("envelope-overwrite-test", nil).(RawEnvelope); !ok {
		t.Fatal("first registration should hit")
	}
	RegisterEnvelope("envelope-overwrite-test", DefaultEnvelope{})
	if _, ok := EnvelopeFor("envelope-overwrite-test", nil).(DefaultEnvelope); !ok {
		t.Fatal("re-registration should overwrite")
	}
}

func TestEnvelopeFor(t *testing.T) {
	RegisterEnvelope("envelope-for-test", DefaultEnvelope{})
	if _, ok := EnvelopeFor("envelope-for-test", nil).(DefaultEnvelope); !ok {
		t.Fatal("named envelope should hit registry")
	}
	got := EnvelopeFor("envelope-for-missing", RawEnvelope{})
	if _, ok := got.(RawEnvelope); !ok {
		t.Fatalf("missing name should fall back: %T", got)
	}
}

func TestRegisterInterceptorPanics(t *testing.T) {
	RegisterInterceptor("itp-test-unique", func(ctx context.Context, ep Endpoint, r RequestReader, s Sink, next func(context.Context) error) error {
		return next(ctx)
	})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate interceptor registration")
		}
	}()
	RegisterInterceptor("itp-test-unique", func(ctx context.Context, ep Endpoint, r RequestReader, s Sink, next func(context.Context) error) error {
		return next(ctx)
	})
}

func TestMustInterceptorPanicsWhenMissing(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on missing interceptor")
		}
	}()
	MustInterceptor("itp-definitely-not-registered-xyz")
}

func TestMustDuration(t *testing.T) {
	if d := MustDuration("5s"); d.Seconds() != 5 {
		t.Fatalf("MustDuration = %v", d)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on invalid duration")
		}
	}()
	MustDuration("5x")
}

// ---- 哨兵与错误映射依赖项（errors_test.go 补充矩阵之外的回归） ----

func TestStatusCoder(t *testing.T) {
	var err error = customCoded{status: 429, msg: "too many"}
	status, code, msg, ok := ResolveErrorStatus(err)
	if !ok || status != 429 || code != CodeError || msg != "too many" {
		t.Fatalf("StatusCoder resolve: (%d, %d, %q, %v)", status, code, msg, ok)
	}
	// errors.As 识别 StatusCoder 接口
	var sc StatusCoder
	if !errors.As(err, &sc) || sc.StatusCode() != 429 {
		t.Fatal("errors.As should find StatusCoder")
	}
}
