package servergin

import (
	"context"

	"github.com/EdSan845D/oapi-hinge/hinge"

	"github.com/gin-gonic/gin"
)

// InterceptAsGin 把内核拦截器适配为 gin 路由链节点，供手写装配的路由使用；
// 生成代码对 oapi:interceptor 统一走 Handle 的 extra 变参进内核链，不经此包装。
// next(ctx) 语义：把拦截器产出的 ctx 注入 c.Request 后执行真实剩余链（c.Next()）。
// 拦截器返回错误时经传入的响应壳写出（壳拥有 (HTTP 状态码, 响应体) 决策权，
// 与内核 Handle 路径同构；建议传入与 k.SetEnvelope 相同的壳实例）。
func InterceptAsGin(ep hinge.Endpoint, in hinge.Interceptor, env hinge.Envelope) gin.HandlerFunc {
	return func(c *gin.Context) {
		rd := &Reader{C: c}
		sw := &Sink{C: c}
		err := in(c.Request.Context(), ep, rd, sw, func(ctx context.Context) error {
			c.Request = c.Request.WithContext(ctx)
			c.Next()
			return nil
		})
		if err != nil {
			status, body := env.Failure(err)
			c.PureJSON(status, body)
		}
	}
}
