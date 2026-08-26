//go:build !openapi

// 运行时入口（release 构建）：原生 Gin 纯净应用。
// fuego 仅作为开发期 OpenAPI 文档生成器（见 main_doc.go，-tags openapi 构建），
// 本构建不包含任何文档生成相关代码与开发期多余依赖。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"fuego-hinge/app/routes"
	"fuego-hinge/internal/servergin"

	scalargo "github.com/bdpiprava/scalar-go"
	"github.com/gin-gonic/gin"
)

func main() {
	addr := flag.String("addr", ":8097", "listen address, e.g 127.0.0.1:8097")
	flag.Parse()

	if os.Getenv("FUEGO_HINGE_ENV") != "dev" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()
	s := servergin.New()

	// 扩展点演示：把 gin 上下文里的用户信息注入 handler 的 context.Context
	// （handler 通过 handlers.CurrentUser(ctx) 读取，业务层零 gin 依赖）
	s.SetContextDecorator(func(c *gin.Context, ctx context.Context) context.Context {
		return ctx
	})

	// 按分组挂载：users 组的 auth 中间件声明在 routes.All() 的 Group.Middlewares
	s.Mount(r.Group(routes.BasePath), routes.All())

	r.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "fuego-hinge server: try /api/health")
	})

	r.GET("/docs", func(ctx *gin.Context) {
		spec, err := os.ReadFile("openapi.yaml")
		if err != nil {
			ctx.AbortWithError(404, errors.New("SPEC文件丢失"))
			return
		}
		html, err := scalargo.NewV2(
			scalargo.WithSpecBytes(spec),
		)
		if err != nil {
			ctx.AbortWithError(555, err)
			return
		}
		fmt.Fprint(ctx.Writer, html)
	})

	log.Printf("fuego-hinge listening on %s", *addr)
	if err := r.Run(*addr); err != nil {
		log.Fatal(err)
	}
}
