//go:build openapi

// 开发期文档生成入口：go run -tags openapi . -out openapi.yaml
// 消费生成表（Endpoints()），release 构建零开发期依赖的哲学不变。
package main

import (
	"flag"
	"fmt"

	"opai-hinge/app/eps"
	"github.com/EdSan845D/oapi-hinge/hinge"
	"github.com/EdSan845D/oapi-hinge/openapi"

	"github.com/getkin/kin-openapi/openapi3"
)

// collect 拼接全部 Enterpoint 的端点表。
func collect(epss ...hinge.Enterpoint) []hinge.Endpoint {
	var out []hinge.Endpoint
	for _, ep := range epss {
		out = append(out, ep.Endpoints()...)
	}
	return out
}

func main() {
	out := flag.String("out", "openapi.yaml", "openapi 文档输出路径（.yaml/.yml -> YAML，.json -> JSON）")
	flag.Parse()

	all := collect(
		eps.SystemEp{},
		eps.UserEp{Store: eps.NewUserStore()},
		eps.FileEp{},
	)

	info := &openapi3.Info{
		Title:       "OAPI_HINGE API",
		Version:     "1.0.0",
		Description: "本规范由端点注解自动生成（hinge gen + Endpoints 表），请勿手改。",
	}
	servers := &openapi3.Servers{{URL: "/api"}}
	security := openapi3.SecuritySchemes{
		"BearerAuth": &openapi3.SecuritySchemeRef{Value: openapi3.NewSecurityScheme().
			WithType("http").
			WithScheme("bearer").
			WithDescription("token 传递方式：Header `Authorization: Bearer <token>`")},
	}
	if err := openapi.Generate(
		*out,
		all,
		openapi.OptionWithDocInfo(info),
		openapi.OptionWithServer(servers),
		openapi.OptionWithSecurity(security),
		openapi.OptionWithSourceComments(),
	); err != nil {
		panic(err)
	}
	fmt.Println("openapi spec written to", *out)
}
