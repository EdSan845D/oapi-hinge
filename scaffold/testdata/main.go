//go:build !openapi

// 运行时入口：v0.2 装配只剩 DI + 一行注册。
// 业务侧没有路由注册代码——注册函数由 hinge gen 从 oapi:* 注解生成（apigen 包）。
// OpenAPI 文档生成走独立构建（见 main_doc.go，-tags openapi），本构建零开发期依赖。
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"opai-hinge/apigen"
	"opai-hinge/app/eps"
	"github.com/EdSan845D/oapi-hinge/hinge"
	"github.com/EdSan845D/oapi-hinge/hinge/validator"
	"github.com/EdSan845D/oapi-hinge/servergin"

	"github.com/gin-gonic/gin"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address, e.g 127.0.0.1:8080")
	flag.Parse()

	if os.Getenv("OAPI_HINGE_ENV") != "dev" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()
	k := servergin.NewKernel()
	k.SetCorrelation(true)

	// 扩展点 1：完整规则校验（validate:"..."，go-playground 引擎）。
	// 只用生成绑定器内置 required + Validate() 的项目无需调用。
	k.AddValidator(validator.Playground())

	// 扩展点 2：oapi:auth BearerAuth 注解引用的拦截器（运行时实现与文档 scheme 同名配对）。
	// OAPI_HINGE_ENV=dev 时跳过鉴权，方便本地联调。
	hinge.RegisterInterceptor("BearerAuth", func(ctx context.Context, ep hinge.Endpoint, req hinge.RequestReader, s hinge.Sink, next func(context.Context) error) error {
		if os.Getenv("OAPI_HINGE_ENV") == "dev" {
			return next(ctx)
		}
		tok, _ := req.Header("Authorization")
		if !strings.HasPrefix(tok, "Bearer ") {
			s.WriteJSON(http.StatusUnauthorized, map[string]any{"code": http.StatusUnauthorized, "data": nil, "msg": "missing bearer token"})
			return nil
		}
		return next(ctx)
	})

	// 装配：DI + 一行注册
	epsAll := apigen.All{
		SystemEp: eps.SystemEp{},
		UserEp:   eps.UserEp{Store: eps.NewUserStore()},
		FileEp:   eps.FileEp{},
	}
	apigen.RegisterAllGin(r.Group("/api"), k, epsAll)

	log.Printf("listening on %s", *addr)
	if err := r.Run(*addr); err != nil {
		log.Fatal(err)
	}
}
