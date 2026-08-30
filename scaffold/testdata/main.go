//go:build !openapi

// 运行时入口（release 构建）：原生 Gin 纯净应用。
// OpenAPI 文档生成走独立构建（见 main_doc.go，-tags openapi），本构建零开发期依赖。
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/EdSan845D/oapi-hinge/contract/validator"
	"github.com/EdSan845D/oapi-hinge/servergin"
	"opai-hinge/app/routes"

	"github.com/gin-gonic/gin"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address, e.g 127.0.0.1:8080")
	flag.Parse()

	if os.Getenv("OAPI_HINGE_ENV") != "dev" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()
	s := servergin.New()

	// 扩展点演示 1：完整规则校验（validate:"..."，go-playground 引擎）。
	// 只用内置 required + Validate() 的项目无需调用。
	s.AddValidator(validator.Playground())

	// 扩展点演示 2（可选）：替换响应壳。默认 {code, data, msg}；
	// 需要纯 RESTful 裸输出时放开下一行，并同步在 main_doc.go 配置文档侧壳 schema。
	// s.SetEnvelope(response.RawEnvelope{})

	// 扩展点演示 3：把 gin 中间件解析出的用户信息注入 handler 的 context.Context
	// （handler 通过 handlers.CurrentUser(ctx) 读取，业务层零 gin 依赖）
	s.SetContextDecorator(func(c *gin.Context, ctx context.Context) context.Context {
		return ctx
	})

	// 按分组挂载：users 组的 auth 中间件声明在 routes.All() 的 Group.Middlewares
	s.Mount(r.Group(routes.BasePath), routes.All())

	r.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "opai-hinge server: try /api/health")
	})

	log.Printf("opai-hinge listening on %s", *addr)
	if err := r.Run(*addr); err != nil {
		log.Fatal(err)
	}
}
