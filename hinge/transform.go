package hinge

import (
	"context"
	"reflect"
)

// InTransformer 入参转换接口：由生成的绑定器在绑定完成后、必填/校验检查前
// 自动调用（生成期已知类型是否实现，零反射）。用于规范化输入
// （trim、大小写归一、默认值填充）；返回错误将短路请求。
//
// 实现建议使用指针接收者（生成绑定器对值取地址调用）：
//
//	func (r *CreateUserReq) InTransform(ctx context.Context) error {
//	    r.Name = strings.TrimSpace(r.Name)
//	    return nil
//	}
type InTransformer interface {
	InTransform(context.Context) error
}

// OutTransformer 出参转换接口：Handler 返回后、序列化之前由内核自动调用。
// 用于输出加工（脱敏、裁剪字段、补充计算字段）；返回错误将短路请求。
//
// 建议使用指针接收者；值接收者也可用（只读场景），但修改不会写回。
type OutTransformer interface {
	OutTransform(context.Context) error
}

// TransformIn 入参转换（手动挂载逃生口使用；生成绑定器在生成期直接内联调用）。
// v 为绑定后的入参值。
func TransformIn(ctx context.Context, v any) error {
	if t, ok := v.(InTransformer); ok {
		return t.InTransform(ctx)
	}
	return nil
}

// TransformOut 出参转换（内核成功路径调用）：v 为 Handler 返回的响应值。
// 返回转换后的值；指针接收者实现且为值类型时，写回拷贝。
// 快路径为类型断言（零分配）；仅「值类型 + 指针接收者」走一次反射拷贝。
func TransformOut(ctx context.Context, v any) (any, error) {
	if v == nil {
		return v, nil
	}
	// 指针 / 值接收者直接断言
	if t, ok := v.(OutTransformer); ok {
		if err := t.OutTransform(ctx); err != nil {
			return v, err
		}
		return v, nil
	}
	// 值类型 + 指针接收者：拷贝到新指针转换，写回拷贝
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Struct {
		tmp := reflect.New(rv.Type())
		tmp.Elem().Set(rv)
		if t, ok := tmp.Interface().(OutTransformer); ok {
			if err := t.OutTransform(ctx); err != nil {
				return v, err
			}
			return tmp.Elem().Interface(), nil
		}
	}
	return v, nil
}
