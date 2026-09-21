package hinge

import (
	"context"
	"errors"
	"testing"
)

// ---- TransformOut 三条路径：值/指针接收者直调、值类型+指针接收者拷贝、错误短路 ----

type outValueRecv struct{ Name string }

func (v outValueRecv) OutTransform(context.Context) error { v2 := v; _ = v2; return nil }

type outPtrRecv struct{ Name string }

func (v *outPtrRecv) OutTransform(context.Context) error {
	v.Name = "ptr-modified"
	return nil
}

type outErrRecv struct{}

func (outErrRecv) OutTransform(context.Context) error { return errors.New("transform boom") }

func TestTransformOutNil(t *testing.T) {
	out, err := TransformOut(context.Background(), nil)
	if err != nil || out != nil {
		t.Fatalf("nil passthrough: (%v, %v)", out, err)
	}
}

func TestTransformOutValueReceiver(t *testing.T) {
	in := outValueRecv{Name: "a"}
	out, err := TransformOut(context.Background(), in)
	if err != nil || out.(outValueRecv).Name != "a" {
		t.Fatalf("value receiver: (%v, %v)", out, err)
	}
}

func TestTransformOutPtrReceiverOnPointer(t *testing.T) {
	in := &outPtrRecv{Name: "a"}
	out, err := TransformOut(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.(*outPtrRecv).Name != "ptr-modified" {
		t.Fatalf("ptr receiver in-place modify lost: %+v", out)
	}
}

func TestTransformOutPtrReceiverOnValueCopies(t *testing.T) {
	// 值类型 + 指针接收者：拷贝到新指针转换、写回拷贝，原值不变
	in := outPtrRecv{Name: "orig"}
	out, err := TransformOut(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.(outPtrRecv).Name; got != "ptr-modified" {
		t.Fatalf("copied value should be transformed: %q", got)
	}
	if in.Name != "orig" {
		t.Fatalf("original value must stay untouched: %q", in.Name)
	}
}

func TestTransformOutErrorShortCircuits(t *testing.T) {
	_, err := TransformOut(context.Background(), outErrRecv{})
	if err == nil || err.Error() != "transform boom" {
		t.Fatalf("transform error = %v", err)
	}
}

// ---- TransformIn（手动逃生口）----

type inTransformable struct{ ok bool }

func (v *inTransformable) InTransform(context.Context) error { v.ok = true; return nil }

func TestTransformIn(t *testing.T) {
	v := &inTransformable{}
	if err := TransformIn(context.Background(), v); err != nil || !v.ok {
		t.Fatalf("TransformIn = (%v, %v)", v.ok, err)
	}
	if err := TransformIn(context.Background(), "not-transformable"); err != nil {
		t.Fatalf("non-transformable should pass: %v", err)
	}
}
