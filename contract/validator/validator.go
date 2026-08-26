// Package validator 参数校验扩展点。
// 校验时机：Q/B 解析完成后、Handler 调用前。
// 内置规则（绑定阶段执行）：
//   - query/path 字段 `binding:"required"`：缺失时报错
//   - Q 或 B 实现 `Validate() error` 接口：绑定后自动调用
// 扩展方式：server.AddValidator(validator.Func) 追加自定义校验器。
package validator

import (
	"context"
	"fmt"
)

// Func 自定义校验器签名。method 为 HTTP 方法；q/b 为解析后的请求值（指针）。
type Func func(ctx context.Context, method string, q, b any) error

// Run 执行校验：先调结构体自身的 Validate() 方法，再依次执行自定义校验器。
// q/b 传指针（与绑定阶段一致）。
func Run(ctx context.Context, method string, q, b any, custom ...Func) error {
	for _, v := range []any{q, b} {
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