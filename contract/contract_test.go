package contract

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
)

// ============ StatusError ============

func TestStatusErrorMessages(t *testing.T) {
	tests := []struct {
		name string
		err  *StatusError
		want string
	}{
		{"msg+err", &StatusError{Status: 404, Msg: "用户不存在", Err: errors.New("db: no rows")}, "用户不存在: db: no rows"},
		{"only msg", &StatusError{Status: 404, Msg: "用户不存在"}, "用户不存在"},
		{"only err", &StatusError{Status: 404, Err: errors.New("no rows")}, "no rows"},
		{"empty", &StatusError{Status: 503}, "http status error (503)"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("%s: Error() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestStatusErrorStatusCode(t *testing.T) {
	if got := (&StatusError{}).StatusCode(); got != http.StatusInternalServerError {
		t.Errorf("zero status = %d, want 500", got)
	}
	if got := (&StatusError{Status: http.StatusTeapot}).StatusCode(); got != http.StatusTeapot {
		t.Errorf("teapot = %d", got)
	}
}

func TestStatusErrorUnwrapAndWithCause(t *testing.T) {
	inner := errors.New("connection refused")
	err := WithCause(NotFound("用户不存在"), inner)

	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusNotFound || se.Msg != "用户不存在" {
		t.Fatalf("WithCause lost fields: %+v", se)
	}
	if !errors.Is(err, inner) {
		t.Fatal("WithCause: Unwrap chain broken")
	}

	// 非 StatusError 原样透传
	plain := errors.New("plain")
	if WithCause(plain, inner) != plain {
		t.Fatal("WithCause should pass through non-StatusError")
	}

	// errors.Unwrap 直达内部错误
	if u := errors.Unwrap(&StatusError{Err: inner}); !errors.Is(u, inner) {
		t.Fatal("Unwrap broken")
	}
}

func TestConvenienceConstructors(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
	}{
		"BadRequest":   {BadRequest("x"), http.StatusBadRequest},
		"Unauthorized": {Unauthorized("x"), http.StatusUnauthorized},
		"Forbidden":    {Forbidden("x"), http.StatusForbidden},
		"NotFound":     {NotFound("x"), http.StatusNotFound},
		"Conflict":     {Conflict("x"), http.StatusConflict},
		"Internal":     {Internal("x"), http.StatusInternalServerError},
	}
	for name, tt := range cases {
		var se *StatusError
		if !errors.As(tt.err, &se) || se.StatusCode() != tt.status {
			t.Errorf("%s: status = %v, want %d", name, se, tt.status)
		}
	}
}

// ============ FuncName ============

func sampleFn(ctx context.Context, q, b int) (int, error) { return 0, nil }

func TestFuncName(t *testing.T) {
	// 返回「包.函数」形态（路径已裁剪）
	if got := FuncName(sampleFn); got != "contract.sampleFn" {
		t.Errorf("FuncName = %q, want contract.sampleFn", got)
	}
	if got := FuncName(nil); got != "" {
		t.Errorf("FuncName(nil) = %q, want empty", got)
	}
}

// ============ CheckHandler ============

func TestCheckHandler(t *testing.T) {
	good := func(ctx context.Context, q, b int) (string, error) { return "", nil }
	if err := CheckHandler(good); err != nil {
		t.Fatalf("good handler rejected: %v", err)
	}

	bad := []struct {
		name string
		fn   any
	}{
		{"nil", nil},
		{"not func", 42},
		{"no inputs", func() (string, error) { return "", nil }},
		{"wrong first input", func(a, b, c int) (string, error) { return "", nil }},
		{"one output", func(ctx context.Context, q, b int) string { return "" }},
		{"second output not error", func(ctx context.Context, q, b int) (string, int) { return "", 0 }},
	}
	for _, tt := range bad {
		if err := CheckHandler(tt.fn); err == nil {
			t.Errorf("%s: expected error, got nil", tt.name)
		}
	}
}

// ============ Route 构造 ============

func TestNewRouteMapping(t *testing.T) {
	h := func(ctx context.Context, q, b int) (string, error) { return "", nil }
	r := New(RouteMeta[int, int, string]{
		Method: "GET", Path: "/x", Summary: "s", DefaultStatusCode: 201, Handler: h,
	})
	if r.Method != "GET" || r.Path != "/x" || r.Summary != "s" || r.DefaultStatusCode != 201 {
		t.Fatalf("route mapping wrong: %+v", r)
	}
	if r.Handler == nil {
		t.Fatal("handler lost")
	}
}

// ============ 入参/出参转换 ============

type inReq struct{ Name string }

func (r *inReq) InTransform(ctx context.Context) error {
	r.Name = "in:" + r.Name
	return nil
}

type outVal struct{ Secret string }

func (o *outVal) OutTransform(ctx context.Context) error {
	o.Secret = "masked"
	return nil
}

func TestTransformIn(t *testing.T) {
	r := &inReq{Name: "x"}
	if err := TransformIn(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if r.Name != "in:x" {
		t.Fatalf("InTransform not called: %q", r.Name)
	}
	// 未实现接口：无副作用、无错误
	n := 1
	if err := TransformIn(context.Background(), &n); err != nil {
		t.Fatalf("plain value should pass through: %v", err)
	}
}

func TestTransformOut(t *testing.T) {
	ctx := context.Background()

	// 值类型 + 指针接收者：拷贝到新指针转换后写回
	v := reflect.ValueOf(outVal{Secret: "raw"})
	tv, err := TransformOut(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	if got := tv.Interface().(outVal).Secret; got != "masked" {
		t.Fatalf("pointer-receiver value transform = %q", got)
	}

	// 指针类型：就地转换
	p := reflect.ValueOf(&outVal{Secret: "raw"})
	tv, err = TransformOut(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if tv.Interface().(*outVal).Secret != "masked" {
		t.Fatal("pointer transform failed")
	}

	// nil 指针透传
	np := reflect.ValueOf((*outVal)(nil))
	tv, err = TransformOut(ctx, np)
	if err != nil {
		t.Fatal(err)
	}
	if tv.Interface().(*outVal) != nil {
		t.Fatal("nil pointer not passed through")
	}

	// nil 接口（Empty/any 占位 return nil）：原样透传
	var e Empty
	tv, err = TransformOut(ctx, reflect.ValueOf(&e).Elem())
	if err != nil {
		t.Fatal(err)
	}
	if tv.Kind() != reflect.Interface || !tv.IsNil() {
		t.Fatalf("nil interface not passed through: kind=%s", tv.Kind())
	}

	// 无效值：透传不 panic
	if _, err := TransformOut(ctx, reflect.Value{}); err != nil {
		t.Fatalf("invalid value should pass: %v", err)
	}
}
