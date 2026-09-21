package middleware

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/EdSan845D/oapi-hinge/hinge"
)

// AccessLog 内核拦截器示例：框架无关（不 import gin/echo），经
// EntryPointConfig.Interceptors 注入 owner 全端点，hinge gen 发射为
// HandleWith 的 extra 实参，嵌入内核拦截链（框架链之后、bind 之前）。
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

// BearerAuth 内核拦截器示例：oapi:interceptor 注解引用（方法级）。
// 短路时自行经 Sink 写出并返回 nil；返回错误则走统一错误链。
// 文档侧由 openapi 生成器按 MWRefs 尾段名与 OptionWithSecurity scheme
// 同名配对，自动推导 security + 401。
func BearerAuth(ctx context.Context, ep hinge.Endpoint, req hinge.RequestReader, s hinge.Sink, next func(context.Context) error) error {
	tok, _ := req.Header("Authorization")
	if !strings.HasPrefix(tok, "Bearer ") {
		s.WriteJSON(http.StatusUnauthorized, map[string]any{"code": http.StatusUnauthorized, "data": nil, "msg": "missing bearer token"})
		return nil
	}
	return next(ctx)
}
