package middleware

import (
	"context"
	"log"
	"time"

	"github.com/EdSan845D/oapi-hinge/hinge"
)

// AccessLog 内核拦截器示例：框架无关（不 import gin/echo），经
// EntryPointConfig.Interceptors 注入 owner 全端点，hinge gen 发射为
// HandleWith 的 []hinge.Interceptor{...} 实参，嵌入 handle 内核链
// （在端点 oapi:middleware 注册名拦截器之前执行）。
//
// 与框架原生中间件严格分通道：gin/echo 路由级横切（c.Set / 路由级 abort）
// 请用框架中间件（oapi:middleware 源码引用或 EntryPointConfig.Middlewares）；
// 需要端点上下文与统一错误链的框架无关逻辑才做拦截器。两条通道不得混排。
func AccessLog(ctx context.Context, ep hinge.Endpoint, r hinge.RequestReader, s hinge.Sink, next func(context.Context) error) error {
	start := time.Now()
	err := next(ctx)
	log.Printf("[hinge] %s %s done in %s err=%v", r.Method(), ep.Path, time.Since(start), err)
	return err
}
