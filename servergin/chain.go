package servergin

import (
	"context"
	"fmt"

	"github.com/EdSan845D/oapi-hinge/hinge"

	"github.com/gin-gonic/gin"
)

// AsGinChain 把 Midllwares 混排列表桥接为 gin 路由链，并把内核包装器追加在链尾。
//
// 元素类型自动识别（装配期一次，请求期零反射）：
//   - hinge.Interceptor      → 包装为 gin.HandlerFunc（next = 真实剩余链 c.Next()，
//     短路 = 自行经 Sink 写出；返回错误经默认壳写出）
//   - gin.HandlerFunc        → 原样入链（gin 原生语义：c.Set/c.Abort 全量可用）
//   - func(*gin.Context)     → 同上
//   - 其他类型               → panic（fail fast，报出位置与类型）
//
// 顺序完全保真：同一条 Midllwares 里 gin 原生与 Interceptor 可任意交错。
// gin 原生中间件跑在真实 *gin.Context 上（c.Set 状态对后续链可见）；
// Interceptor 的 next(ctx) 会把 ctx 注入 c.Request 后继续真实链。
func AsGinChain(ep hinge.Endpoint, mws []any) []gin.HandlerFunc {
	chain := make([]gin.HandlerFunc, 0, len(mws))
	for i, mw := range mws {
		switch v := mw.(type) {
		case hinge.Interceptor:
			chain = append(chain, InterceptAsGin(ep, v))
		case gin.HandlerFunc:
			chain = append(chain, v)
		case func(*gin.Context):
			chain = append(chain, v)
		default:
			panic(fmt.Sprintf("servergin: Midllwares[%d] 类型 %T 不支持（支持 hinge.Interceptor / gin.HandlerFunc / func(*gin.Context)；具名包级函数可被 hinge gen 引用）", i, mw))
		}
	}
	return chain
}

// InterceptAsGin 把内核拦截器适配为 gin 路由链节点。
// next(ctx) 语义：把拦截器产出的 ctx 注入 c.Request 后执行真实剩余链（c.Next()）。
// 拦截器返回错误时经默认壳写出（路由链无错误通道；需要完整错误链的拦截器
// 请经 //oapi:middleware 名字走内核拦截器注册表）。
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
