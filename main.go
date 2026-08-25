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
	"strings"

	"fuego-hinge/app/routes"
	"fuego-hinge/internal/server"

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
	s := server.New()

	// ============ 扩展点演示：全局中间件 ============
	// 替换为你的真实鉴权（JWT / OAuth / session）。示例：Bearer token 校验，
	// FUEGO_HINGE_ENV=dev 时跳过（与文档生成器的 Auth 标注配合使用）。
	s.Use(func(c *gin.Context) {
		if os.Getenv("FUEGO_HINGE_ENV") == "dev" {
			c.Next()
			return
		}
		token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if token == "" || token != "demo-token" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	})

	// 扩展点演示：把 gin 上下文里的用户信息注入 handler 的 context.Context
	// （handler 通过 handlers.CurrentUser(ctx) 读取，业务层零 gin 依赖）
	s.SetContextDecorator(func(c *gin.Context, ctx context.Context) context.Context {
		return ctx
	})

	s.Mount(r.Group("/api"), routes.All())

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
