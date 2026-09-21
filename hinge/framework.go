package hinge

import (
	"context"
)

// frameworkKey 框架上下文 key。适配器的默认装饰器把原生上下文对象
// （*gin.Context / echo.Context / *http.Request）存入 ctx，
// 业务层按需断言使用——代价是与框架耦合，仅限无法模板化的少数场景。
type frameworkKey struct{}

// WithFramework 注入框架上下文对象（适配器默认装饰器调用）。
func WithFramework(ctx context.Context, fw any) context.Context {
	return context.WithValue(ctx, frameworkKey{}, fw)
}

// Framework 取出框架上下文对象；未注入时返回 nil。
func Framework(ctx context.Context) any {
	return ctx.Value(frameworkKey{})
}
