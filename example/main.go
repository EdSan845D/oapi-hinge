//go:build !openapi

// 运行时入口：v0.2 装配只剩 DI + 一行注册。
// 业务侧没有路由注册代码——注册函数由 hinge gen 从 oapi:* 注解生成（apigen 包）。
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

	"github.com/EdSan845D/oapi-hinge/example/apigen"
	"github.com/EdSan845D/oapi-hinge/example/app/eps"
	"github.com/EdSan845D/oapi-hinge/hinge"
	"github.com/EdSan845D/oapi-hinge/hinge/validator"
	"github.com/EdSan845D/oapi-hinge/servergin"

	scalargo "github.com/bdpiprava/scalar-go"
	"github.com/gin-gonic/gin"
)

func main() {
	addr := flag.String("addr", ":8097", "listen address, e.g 127.0.0.1:8097")
	flag.Parse()

	if os.Getenv("GIN_ENV") != "dev" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()
	k := servergin.NewKernel()
	k.SetCorrelation(true)

	// 扩展点 1：完整规则校验（validate:"..."，go-playground 引擎）。
	// 只用生成绑定器内置 required + Validate() 的项目无需调用。
	k.AddValidator(validator.Playground())

	// 扩展点 2：oapi:auth BearerAuth 注解引用的拦截器（运行时实现与文档 scheme 同名配对）。
	// 短路时自行经 Sink 写出并返回 nil；返回错误则走统一错误链。
	hinge.RegisterInterceptor("BearerAuth", func(ctx context.Context, ep hinge.Endpoint, req hinge.RequestReader, s hinge.Sink, next func(context.Context) error) error {
		tok, _ := req.Header("Authorization")
		if !strings.HasPrefix(tok, "Bearer ") {
			s.WriteJSON(http.StatusUnauthorized, map[string]any{"code": http.StatusUnauthorized, "data": nil, "msg": "missing bearer token"})
			return nil
		}
		return next(ctx)
	})

	// 扩展点 3（可选）：替换默认响应壳 / 绑定失败状态码 / 关联 ID 等
	// k.SetEnvelope(hinge.RawEnvelope{})
	// k.SetBindErrorStatus(http.StatusBadRequest)

	// 装配：DI + 一行注册（gin / echo / http 各自的 RegisterAll 已生成）
	epsAll := apigen.All{
		SystemEp: eps.SystemEp{},
		UserEp:   eps.UserEp{Store: eps.NewUserStore()},
		FileEp:   eps.FileEp{},
	}
	apigen.RegisterAllGin(r.Group("/api"), k, epsAll)

	r.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "oapi-hinge server: try /api/health")
	})
	r.GET("/docs", func(c *gin.Context) {
		spec, err := os.ReadFile("openapi.yaml")
		if err != nil {
			c.AbortWithError(404, errors.New("SPEC文件丢失"))
			return
		}
		html, err := scalargo.NewV2(scalargo.WithSpecBytes(spec))
		if err != nil {
			c.AbortWithError(555, err)
			return
		}
		fmt.Fprint(c.Writer, html)
	})

	log.Printf("oapi-hinge listening on %s", *addr)
	if err := r.Run(*addr); err != nil {
		log.Fatal(err)
	}
}
