package serverecho

import (
	"context"

	"github.com/EdSan845D/oapi-hinge/hinge"

	"github.com/labstack/echo/v4"
)

// InterceptAsEcho 把单个内核拦截器适配为 echo 路由中间件节点，供手写装配的
// 路由使用；生成代码对 oapi:interceptor 统一走 Handle 的 extra 变参进内核链，
// 不经此包装。
// next(ctx) 语义与 servergin.InterceptAsGin 对称。
func InterceptAsEcho(ep hinge.Endpoint, in hinge.Interceptor) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			rd := &Reader{C: c}
			sw := &Sink{C: c}
			return in(c.Request().Context(), ep, rd, sw, func(ctx context.Context) error {
				c.SetRequest(c.Request().WithContext(ctx))
				return next(c)
			})
		}
	}
}
