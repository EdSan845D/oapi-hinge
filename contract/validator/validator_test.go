package validator

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type vInner struct {
	City string `json:"city" binding:"required"`
}

// ============ isNil ============

func TestIsNil(t *testing.T) {
	var nilPtr *vInner
	var nilIface any = nilPtr
	var nilMap map[string]string
	var nilSlice []string
	var pi *any // (*interface{})(nil)：适配器对 Q=interface 生成的形态

	cases := []struct {
		name string
		v    any
		want bool
	}{
		{"untyped nil", nil, true},
		{"typed nil pointer", nilPtr, true},
		{"interface holding nil pointer", nilIface, true},
		{"(*interface{})(nil)", pi, true},
		{"nil map", nilMap, true},
		{"nil slice", nilSlice, true},
		{"struct pointer", &vInner{}, false},
		{"int", 42, false},
		{"string", "x", false},
	}
	for _, tt := range cases {
		if got := isNil(tt.v); got != tt.want {
			t.Errorf("%s: isNil = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// ============ checkRequired ============

func TestCheckRequired(t *testing.T) {
	// 匿名内嵌结构体递归
	req := &struct {
		Name string `json:"name" binding:"required"`
		vInner
	}{Name: "x"}
	if err := checkRequired(req); err == nil || !strings.Contains(err.Error(), "city") {
		t.Fatalf("embedded struct required not checked: %v", err)
	}

	// 字段名取 json tag（缺省回退字段名）
	req2 := &struct {
		Age int `json:"user_age" validate:"required"`
	}{}
	if err := checkRequired(req2); err == nil || !strings.Contains(err.Error(), "user_age") {
		t.Fatalf("json tag field name expected: %v", err)
	}
	req3 := &struct {
		Age int `validate:"required"`
	}{}
	if err := checkRequired(req3); err == nil || !strings.Contains(err.Error(), "Age") {
		t.Fatalf("fallback field name expected: %v", err)
	}

	// 非匿名嵌套结构体不递归（完整递归用 Playground 校验器）
	type outer struct{ Nest vInner }
	if err := checkRequired(&outer{}); err != nil {
		t.Fatalf("non-anonymous struct should not be recursed: %v", err)
	}

	// 通过场景
	ok := &struct {
		Name string `json:"name" binding:"required"`
	}{Name: "x"}
	if err := checkRequired(ok); err != nil {
		t.Fatalf("valid struct should pass: %v", err)
	}

	// 非结构体指针 / nil 指针 / 值类型：跳过
	if err := checkRequired((*vInner)(nil)); err != nil {
		t.Fatalf("nil pointer should skip: %v", err)
	}
	if err := checkRequired(vInner{}); err != nil {
		t.Fatalf("non-pointer should skip: %v", err)
	}
	if err := checkRequired(42); err != nil {
		t.Fatalf("non-pointer non-struct should skip: %v", err)
	}
}

// ============ Run ============

type vValidateReq struct{ Token string }

func (r *vValidateReq) Validate() error {
	if r.Token == "" {
		return errors.New("token empty")
	}
	return nil
}

func TestRunValidateInterface(t *testing.T) {
	err := Run(context.Background(), "POST", &vValidateReq{}, nil)
	if err == nil || !strings.Contains(err.Error(), "token empty") {
		t.Fatalf("Validate() not invoked: %v", err)
	}
	// 通过场景
	if err := Run(context.Background(), "POST", &vValidateReq{Token: "t"}, nil); err != nil {
		t.Fatalf("valid should pass: %v", err)
	}
}

func TestRunCustomValidators(t *testing.T) {
	var order []string
	va := Func(func(ctx context.Context, method string, q, b any) error {
		order = append(order, "a")
		return nil
	})
	vb := Func(func(ctx context.Context, method string, q, b any) error {
		order = append(order, "b")
		return errors.New("boom")
	})

	// 按注册顺序执行，首个错误短路
	err := Run(context.Background(), "GET", &vInner{City: "x"}, nil, va, vb)
	if err == nil || err.Error() != "boom" || len(order) != 2 {
		t.Fatalf("custom validators: err=%v order=%v", err, order)
	}

	// method 透传
	var gotMethod string
	vm := Func(func(ctx context.Context, method string, q, b any) error {
		gotMethod = method
		return nil
	})
	if err := Run(context.Background(), "DELETE", nil, nil, vm); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Fatalf("method not passed: %q", gotMethod)
	}

	// nil 入参跳过内置检查，自定义校验器照常执行
	ran := false
	vn := Func(func(ctx context.Context, method string, q, b any) error {
		ran = true
		return nil
	})
	if err := Run(context.Background(), "GET", nil, nil, vn); err != nil || !ran {
		t.Fatalf("nil q/b handling: err=%v ran=%v", err, ran)
	}
}

// ============ IsRequired ============

func TestIsRequired(t *testing.T) {
	typ := reflect.TypeOf(struct {
		A string `binding:"required"`
		B string `validate:"required,min=1"`
		C string `binding:"min=1"`
	}{})
	if !IsRequired(typ.Field(0)) || !IsRequired(typ.Field(1)) || IsRequired(typ.Field(2)) {
		t.Fatal("IsRequired tag parsing broken")
	}
}
