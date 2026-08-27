package contract

import (
	"context"
	"reflect"
)

// InTransformer 入参转换接口：Q/B 绑定完成后、校验之前被适配器自动调用。
// 用于规范化输入（trim、大小写归一、默认值填充）；返回错误将短路请求。
//
// 实现建议使用指针接收者（与 B 的绑定值一致，修改直接生效）：
//
//	func (r *CreateUserReq) InTransform(ctx context.Context) error {
//	    r.Name = strings.TrimSpace(r.Name)
//	    return nil
//	}
type InTransformer interface {
	InTransform(context.Context) error
}

// OutTransformer 出参转换接口：Handler 返回后、序列化之前被适配器自动调用。
// 用于输出加工（脱敏、裁剪字段、补充计算字段）；返回错误将短路请求。
//
// 建议使用指针接收者；值接收者也可用（只读场景），但修改不会写回：
//
//	func (u *User) OutTransform(ctx context.Context) error {
//	    u.Password = "******"
//	    return nil
//	}
type OutTransformer interface {
	OutTransform(context.Context) error
}

// TransformIn 入参转换（适配器内部使用）：v 为绑定后的入参（*Q / *B 指针）。
func TransformIn(ctx context.Context, v any) error {
	if t, ok := v.(InTransformer); ok {
		return t.InTransform(ctx)
	}
	return nil
}

// TransformOut 出参转换（适配器内部使用）：v 为 Handler 返回的响应值
// （reflect.Call 的返回值，值类型可能不可寻址）。
// 返回转换后的值；指针接收者实现且为值类型时，写回拷贝。
func TransformOut(ctx context.Context, v reflect.Value) (reflect.Value, error) {
	if !v.IsValid() {
		return v, nil
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return v, nil
		}
		if t, ok := v.Interface().(OutTransformer); ok {
			if err := t.OutTransform(ctx); err != nil {
				return v, err
			}
		}
		return v, nil
	}
	// 值类型：优先尝试取地址（指针接收者实现，写回拷贝）；
	// 不可寻址（reflect.Call 返回值）时拷贝到新指针再转换
	var tmp reflect.Value
	switch {
	case v.CanAddr():
		tmp = v.Addr()
	default:
		tmp = reflect.New(v.Type())
		tmp.Elem().Set(v)
	}
	if t, ok := tmp.Interface().(OutTransformer); ok {
		if err := t.OutTransform(ctx); err != nil {
			return v, err
		}
		if !v.CanAddr() {
			return tmp.Elem(), nil
		}
		return v, nil
	}
	// 值接收者实现：只读调用（修改不写回）
	if t, ok := v.Interface().(OutTransformer); ok {
		if err := t.OutTransform(ctx); err != nil {
			return v, err
		}
	}
	return v, nil
}
