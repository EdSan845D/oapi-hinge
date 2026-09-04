//go:build openapi

// 开发期文档生成入口：go run -tags openapi ./example -out openapi.yaml
// 消费生成表（Endpoints()），运行时零开发期依赖的哲学不变。
package main

import (
	"flag"
	"fmt"

	"github.com/EdSan845D/oapi-hinge/example/apigen"
	"github.com/EdSan845D/oapi-hinge/openapi"

	"github.com/getkin/kin-openapi/openapi3"
)

func main() {
	out := flag.String("out", "openapi.yaml", "openapi 文档输出路径（.yaml/.yml -> YAML，.json -> JSON）")
	flag.Parse()

	info := &openapi3.Info{
		Title:       "OAPI-hinge API",
		Version:     "2.0.0",
		Description: "本规范由端点注解自动生成（hinge gen + Endpoints 表），请勿手改。",
	}
	servers := &openapi3.Servers{{URL: "/api"}}
	// scheme 名与 oapi:auth 注解值（Endpoint.Auth）同名配对
	security := openapi3.SecuritySchemes{
		"BearerAuth": &openapi3.SecuritySchemeRef{Value: openapi3.NewSecurityScheme().
			WithType("http").
			WithScheme("bearer").
			WithDescription("token 传递方式：Header `Authorization: Bearer <token>`")},
	}
	if err := openapi.Generate(
		*out,
		apigen.AllSpecs(),
		openapi.OptionWithDocInfo(info),
		openapi.OptionWithServer(servers),
		openapi.OptionWithSecurity(security),
		openapi.OptionWithSourceComments(), // 注释即文档：字段/结构体注释进描述
	); err != nil {
		panic(err)
	}
	fmt.Println("openapi spec written to", *out)
}
