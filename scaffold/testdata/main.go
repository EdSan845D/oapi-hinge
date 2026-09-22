// 运行时入口：v0.2 装配只剩 DI + 一行注册。
// 业务侧没有路由注册代码——注册函数由 hinge gen 从 oapi:* 注解生成（apigen 包）。
// OpenAPI 文档生成走独立入口 docs/（go run ./docs），运行时二进制零开发期依赖。
package main

import (
	"flag"
	"log"
	"os"

	"github.com/EdSan845D/oapi-hinge/servergin"
	"github.com/EdSan845D/oapi-hinge/validator"
	"opai-hinge/apigen"
	"opai-hinge/app/eps"

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

	// 扩展点 2：内核拦截器直引具名函数（无注册表）：UserEp 结构体级
	// oapi:interceptor middleware.BearerAuth → 发射为 HandleWith 的 extra
	// 实参，进内核拦截链。实现见 app/middleware/bearer.go。
	// 文档侧由 openapi 生成器按 MWRefs 尾段名与 OptionWithSecurity scheme 配对。

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
