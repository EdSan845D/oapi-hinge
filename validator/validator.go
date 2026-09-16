// Package validator 参数校验扩展点。
//
// 校验时机：Q/B 绑定完成后、Handler 调用前。
// v0.2 生成的绑定器已内置：来源标签必填检查（binding/validate 双标签）与
// Validate() 方法直调（生成期已知类型，零反射）；本包只承载注入的自定义校验器。
//
// 扩展方式：
//   - Kernel.AddValidator(validator.Func) 追加自定义校验器
//   - Kernel.AddValidator(validator.Playground()) 接入 go-playground/validator
//     （validate:"..." 完整规则）；不调用则不引入该依赖
package validator

import (
	"reflect"

	"github.com/EdSan845D/oapi-hinge/hinge"
)

// Func 自定义校验器签名：与 Kernel.AddValidator 直接对齐（类型别名）。
type Func = hinge.ValidatorFunc

// isNil 判断 any 是否为 nil：包含接口为 nil，以及底层为 nil 的指针/切片/映射等。
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
