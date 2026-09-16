package validator

import (
	"context"
	"fmt"
	"reflect"

	"github.com/EdSan845D/oapi-hinge/hinge"
	"github.com/go-playground/validator/v10"
)

// playground 包级单例（validator.New 开销大，复用实例）
var playground = validator.New()

// Playground 返回 go-playground/validator 封装校验器：支持 validate:"..." 完整规则
// （required、email、min、max、oneof、自定义 tag 等）。
//
// 使用：kernel.AddValidator(validator.Playground())
//
// 注意：调用本函数会引入 go-playground/validator 依赖（仅被 import 时才编译进二进制）；
// 只使用内置 required + Validate() 的项目无需调用。
func Playground() Func {
	return func(ctx context.Context, ep hinge.Endpoint, q, b any) error {
		for _, v := range []any{q, b} {
			if isNil(v) {
				continue
			}
			// 绑定器传入的是值；go-playground 结构体校验用指针（可寻址 + 指针接收者规则）
			vv := v
			if rv := reflect.ValueOf(v); rv.Kind() == reflect.Struct {
				tmp := reflect.New(rv.Type())
				tmp.Elem().Set(rv)
				vv = tmp.Interface()
			}
			if err := playground.Struct(vv); err != nil {
				return fmt.Errorf("validate failed: %w", err)
			}
		}
		return nil
	}
}
