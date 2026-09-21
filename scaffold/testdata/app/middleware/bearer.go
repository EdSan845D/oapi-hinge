package middleware

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/EdSan845D/oapi-hinge/hinge"
)

// BearerAuth 内核拦截器：oapi:interceptor 注解引用（方法级/结构体级均可）。
// 短路时自行经 Sink 写出并返回 nil；返回错误则走统一错误链。
// OAPI_HINGE_ENV=dev 时跳过鉴权，方便本地联调。
// 文档侧由 openapi 生成器按 MWRefs 尾段名与 OptionWithSecurity scheme
// 同名配对，自动推导 security + 401。
func BearerAuth(ctx context.Context, ep hinge.Endpoint, req hinge.RequestReader, s hinge.Sink, next func(context.Context) error) error {
	if os.Getenv("OAPI_HINGE_ENV") == "dev" {
		return next(ctx)
	}
	tok, _ := req.Header("Authorization")
	if !strings.HasPrefix(tok, "Bearer ") {
		s.WriteJSON(http.StatusUnauthorized, map[string]any{"code": http.StatusUnauthorized, "data": nil, "msg": "missing bearer token"})
		return nil
	}
	return next(ctx)
}
