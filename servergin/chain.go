package servergin

import (
	"context"

	"github.com/EdSan845D/oapi-hinge/hinge"

	"github.com/gin-gonic/gin"
)

// InterceptAsGin 把内核拦截器适配为 gin 路由链节点（手写逃生口：生成代码
// 对 oapi:interceptor 统一走 Handle 的 extra 变参进内核链，不经此包装）。
// next(ctx) 语义：把拦截器产出的 ctx 注入 c.Request 后执行真实剩余链（c.Next()）。
// 拦截器返回错误时经默认壳写出（路由链无错误通道；需要统一错误链的
// 拦截器请用 oapi:interceptor 注解，走 HandleWith 的 extra 内核链）。
func InterceptAsGin(ep hinge.Endpoint, in hinge.Interceptor) gin.HandlerFunc {
	return func(c *gin.Context) {
		rd := &Reader{C: c}
		sw := &Sink{C: c}
		err := in(c.Request.Context(), ep, rd, sw, func(ctx context.Context) error {
			c.Request = c.Request.WithContext(ctx)
			c.Next()
			return nil
		})
		if err != nil {
			status, code, msg := hinge.ResolveError(nil, err)
			c.PureJSON(status, map[string]any{"code": code, "data": nil, "msg": msg})
		}
	}
}
