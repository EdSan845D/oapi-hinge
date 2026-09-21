//go:build openapi

// 开发期文档生成入口：go run -tags openapi . -out openapi.yaml
// 消费文档描述表（AllDocSpecs()，docs_gen.go）——仅本入口链接文档元数据，
// 运行时二进制（默认 tag）零文档开销的哲学不变。
package main

import (
	"flag"
	"fmt"

	"github.com/EdSan845D/oapi-hinge/openapi"
	"opai-hinge/apigen"

	"github.com/getkin/kin-openapi/openapi3"
)

func main() {
	out := flag.String("out", "openapi.yaml", "openapi 文档输出路径（.yaml/.yml -> YAML，.json -> JSON）")
	flag.Parse()

	info := &openapi3.Info{
		Title:       "OAPI_HINGE API",
		Version:     "1.0.0",
		Description: "本规范由端点注解自动生成（hinge gen + 端点表），请勿手改。",
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
		apigen.AllDocSpecs(),
		openapi.OptionWithDocInfo(info),
		openapi.OptionWithServer(servers),
		openapi.OptionWithSecurity(security),
		openapi.OptionWithSourceComments(), // 注释即文档：字段/结构体注释进描述
	); err != nil {
		panic(err)
	}
	fmt.Println("openapi spec written to", *out)
}
