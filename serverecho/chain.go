package serverecho

import (
	"context"
	"fmt"

	"github.com/EdSan845D/oapi-hinge/hinge"

	"github.com/labstack/echo/v4"
)

// AsEchoChain 把 Midllwares 混排列表桥接为 echo 路由中间件链，内核包装器由调用方追加。
//
// 元素类型自动识别（装配期一次，请求期零反射）：
//   - hinge.Interceptor   → InterceptAsEcho 包装
//   - echo.MiddlewareFunc → 原样入链
//   - 其他类型            → panic（fail fast）
func AsEchoChain(ep hinge.Endpoint, mws []any) []echo.MiddlewareFunc {
	chain := make([]echo.MiddlewareFunc, 0, len(mws))
	for i, mw := range mws {
		switch v := mw.(type) {
		case hinge.Interceptor:
			chain = append(chain, InterceptAsEcho(ep, v))
		case echo.MiddlewareFunc:
			chain = append(chain, v)
		default:
			panic(fmt.Sprintf("serverecho: Midllwares[%d] 类型 %T 不支持（支持 hinge.Interceptor / echo.MiddlewareFunc）", i, mw))
		}
	}
	return chain
}

// InterceptAsEcho 把单个内核拦截器适配为 echo 路由中间件节点。
// 生成器对 EntryPointConfig 中的 Interceptor 值发射本形态（显式包装，
// 免去 []any 运行时识别）；next(ctx) 语义与 servergin.InterceptAsGin 对称。
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

// HandleWith 同 Handle，并把 Midllwares 混排链组合在外层
// （echo.Router.Add 非变参，链在包装器外组合）。
func HandleWith(k *hinge.Kernel, ep hinge.Endpoint, bindQ, bindB hinge.Binder, h hinge.HandlerFunc, mws ...any) echo.HandlerFunc {
	inner := Handle(k, ep, bindQ, bindB, h)
	for i := len(mws) - 1; i >= 0; i-- {
		switch v := mws[i].(type) {
		case hinge.Interceptor:
			in := v
			prev := inner
			inner = func(c echo.Context) error {
				rd := &Reader{C: c}
				sw := &Sink{C: c}
				return in(c.Request().Context(), ep, rd, sw, func(ctx context.Context) error {
					c.SetRequest(c.Request().WithContext(ctx))
					return prev(c)
				})
			}
		case echo.MiddlewareFunc:
			inner = v(inner)
		default:
			panic(fmt.Sprintf("serverecho: Midllwares 元素类型 %T 不支持", mws[i]))
		}
	}
	return inner
}
