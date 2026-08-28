// Package validator 参数校验扩展点。
// 校验时机：Q/B 解析完成后、Handler 调用前（InTransform 之后）。
// 内置规则（绑定阶段执行）：
//   - query/path/header/body 字段 `binding:"required"` 或 `validate:"required"`：缺失时报错
//   - Q 或 B 实现 `Validate() error` 接口：绑定后自动调用
//
// 扩展方式：
//   - server.AddValidator(validator.Func) 追加自定义校验器
//   - server.AddValidator(validator.Playground()) 接入 go-playground/validator（validate:"..." 完整规则），
//     不调用则不引入该依赖
package validator

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

// Func 自定义校验器签名。method 为 HTTP 方法；q/b 为解析后的请求值（指针）。
type Func func(ctx context.Context, method string, q, b any) error

// Run 执行校验（gin/echo 适配器共用，消除两份重复实现）：
//  1. q/b 结构体的必填标签检查（binding:"required" / validate:"required"）
//  2. q/b 自带 Validate() error 方法
//  3. 依次执行自定义校验器
//
// q/b 传指针（与绑定阶段一致）。
func Run(ctx context.Context, method string, q, b any, custom ...Func) error {
	for _, v := range []any{q, b} {
		// nil（含底层为 nil 的指针/接口等）直接跳过：避免误判必填、nil 接收者 panic。
		if isNil(v) {
			continue
		}
		if err := checkRequired(v); err != nil {
			return err
		}
		if vv, ok := v.(interface{ Validate() error }); ok {
			if err := vv.Validate(); err != nil {
				return fmt.Errorf("validate failed: %w", err)
			}
		}
	}
	for _, fn := range custom {
		if err := fn(ctx, method, q, b); err != nil {
			return err
		}
	}
	return nil
}

// isNil 判断 any 是否为 nil：包含接口为 nil，以及底层为 nil 的指针/切片/映射/chan/func/接口。
// 适配器对 Q/B 类型为 interface{}（如 any/占位）时生成 (*interface{})(nil)，v == nil 判断不到，
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	}
	return false
}

// checkRequired 检查结构体必填标签（binding:"required" 或 validate:"required"）。
// 指针/内嵌结构体递归；对外字段名取 json tag（缺省回退字段名）。
func checkRequired(req any) error {
	rv := reflect.ValueOf(req)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil
	}
	e := rv.Elem()
	if e.Kind() != reflect.Struct {
		return nil
	}
	var walk func(v reflect.Value) error
	walk = func(v reflect.Value) error {
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f, ft := v.Field(i), t.Field(i)
			if ft.Anonymous {
				// 内嵌结构体递归（含未导出类型的内嵌，如 type Req struct { base; ... }：
				// 反射允许对未导出内嵌结构体的导出字段写入）
				sub := f
				if sub.Kind() == reflect.Pointer {
					if sub.IsNil() {
						continue
					}
					sub = sub.Elem()
				}
				if sub.Kind() == reflect.Struct {
					if err := walk(sub); err != nil {
						return err
					}
				}
				continue
			}
			if !ft.IsExported() {
				continue
			}
			if IsRequired(ft) && f.IsZero() {
				return fmt.Errorf("%s is required", fieldName(ft))
			}
		}
		return nil
	}
	return walk(e)
}

// IsRequired 判断字段是否声明必填（binding / validate 双标签兼容）
func IsRequired(ft reflect.StructField) bool {
	return strings.Contains(ft.Tag.Get("binding"), "required") ||
		strings.Contains(ft.Tag.Get("validate"), "required")
}

func fieldName(ft reflect.StructField) string {
	name := strings.Split(ft.Tag.Get("json"), ",")[0]
	if name == "" || name == "-" {
		return ft.Name
	}
	return name
}
