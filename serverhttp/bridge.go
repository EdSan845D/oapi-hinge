package serverhttp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/EdSan845D/oapi-hinge/hinge"
)

// AsInterceptors 把 Midllwares 混排列表桥接为内核拦截器（stdlib 无路由链，
// 统一进内核拦截链；kernel.HandleWith 接收）。
//
// 元素类型自动识别（装配期一次，请求期零反射）：
//   - hinge.Interceptor                    → 原样
//   - func(http.Handler) http.Handler      → 标准库中间件形态，包装为拦截器
//     （Writer/Request 由适配器桥接，next ctx 注入 req）
//   - 其他类型                             → panic（fail fast）
//
// 注意：裸 func(http.ResponseWriter, *http.Request) 没有 next 语义，不能做中间件，
// 请使用 func(http.Handler) http.Handler 形态。
func AsInterceptors(ep hinge.Endpoint, mws []any) []hinge.Interceptor {
	out := make([]hinge.Interceptor, 0, len(mws))
	for i, mw := range mws {
		switch v := mw.(type) {
		case hinge.Interceptor:
			out = append(out, v)
		case func(http.Handler) http.Handler:
			mwf := v
			out = append(out, func(ctx context.Context, ep hinge.Endpoint, r hinge.RequestReader, s hinge.Sink, next func(context.Context) error) error {
				rd, ok1 := r.(*Reader)
				sw, ok2 := s.(*Sink)
				if !ok1 || !ok2 {
					return fmt.Errorf("serverhttp: 中间件需要与本适配器的 Reader/Sink 配对使用")
				}
				h := mwf(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					sw.W = w
					rd.R = req
					_ = next(req.Context())
				}))
				h.ServeHTTP(sw.W, rd.R)
				return nil
			})
		default:
			panic(fmt.Sprintf("serverhttp: Midllwares[%d] 类型 %T 不支持（支持 hinge.Interceptor / func(http.Handler) http.Handler）", i, mw))
		}
	}
	return out
}
