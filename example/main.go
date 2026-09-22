// 运行时入口：v0.2 装配只剩 DI + 一行注册。
// 业务侧没有路由注册代码——注册函数由 hinge gen 从 oapi:* 注解生成（apigen 包）。
// OpenAPI 文档生成走独立入口 docs/（go run ./docs），运行时二进制零文档依赖。
package main

//go:generate go run ./app/generate.go

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/EdSan845D/oapi-hinge/example/apigen"
	"github.com/EdSan845D/oapi-hinge/example/app/eps"
	"github.com/EdSan845D/oapi-hinge/servergin"
	"github.com/EdSan845D/oapi-hinge/validator"

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

	// 扩展点 2：内核拦截器与框架中间件二分引用（无注册表，直引具名函数）：
	//   - oapi:interceptor middleware.BearerAuth（方法级注解，见 app/eps）→
	//     发射为 HandleWith 的 extra 实参，进内核拦截链；
	//   - EntryPointConfig.Interceptors / Middlewares 程序化注入（见 app/generate.go）。
	// 文档侧由 openapi 生成器按 MWRefs 尾段名与 OptionWithSecurity scheme 配对。

	// 扩展点 3（可选）：默认裸输出（RawEnvelope，REST 风格，不加包装器）；
	// 需要 {code,data,msg} 统一壳时显式开启。业务码取值由业务层配置，
	// 框架不内置 CodeOK/CodeError 之类的常量：
	// k.SetEnvelope(hinge.BizCodeEnvelope{OKCode: 0, ErrCode: 10000, SuccessMsg: "ok"})
	// 失败侧 (HTTP 状态码, 响应体) 由壳的 Failure(err) 全权决定；
	// 错误自带状态码（hinge.NotFound 等）始终优先。

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
