//go:build openapi

// 开发期文档生成入口：go run -tags openapi ./example -out openapi.yaml
// 消费生成表（Endpoints()），运行时零开发期依赖的哲学不变。
package main

import (
	"flag"
	"fmt"

	"github.com/EdSan845D/oapi-hinge/example/apigen"
	"github.com/EdSan845D/oapi-hinge/example/app/middleware"
	"github.com/EdSan845D/oapi-hinge/openapi"

	"github.com/getkin/kin-openapi/openapi3"
)

//go:generate go run ./app/generate.go
func main() {
	out := flag.String("out", "openapi.yaml", "openapi 文档输出路径（.yaml/.yml -> YAML，.json -> JSON）")
	flag.Parse()

	info := &openapi3.Info{
		Title:       "OAPI-hinge API",
		Version:     "2.0.0",
		Description: "本规范由端点注解自动生成（hinge gen + Endpoints 表），请勿手改。",
	}
	servers := &openapi3.Servers{{URL: "/api"}}
	// scheme 名与鉴权中间件钩子写出的 security 同名配对
	security := openapi3.SecuritySchemes{
		"BearerAuth": &openapi3.SecuritySchemeRef{Value: openapi3.NewSecurityScheme().
			WithType("http").
			WithScheme("bearer").
			WithDescription("token 传递方式：Header `Authorization: Bearer <token>`")},
	}
	// 中间件文档钩子：按函数引用注册（反射名与生成侧 MWRefs 对齐，
	// 无需手写字符串）。引用了该中间件的端点生成 operation 时调用钩子。
	openapi.RegisterMiddlewareDoc(middleware.Auth, func(op *openapi3.Operation) {
		op.Security = &openapi3.SecurityRequirements{{"BearerAuth": {}}}
		op.Responses.Set("401", &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription("Unauthorized：缺少或无效的 Bearer token")})
	})
	openapi.RegisterMiddlewareDoc(middleware.ParseHeaderWithInfo, func(op *openapi3.Operation) {
		op.AddParameter(&openapi3.Parameter{
			Name: "X-SessionId", In: "header", Required: true,
			Description: "会话 ID（ParseHeaderWithInfo 校验，缺失返回 403）",
			Schema:      &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
		})
		op.Responses.Set("403", &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription("Forbidden：缺少 X-SessionId 请求头")})
	})
	if err := openapi.Generate(
		*out,
		apigen.AllSpecs(),
		openapi.OptionWithDocInfo(info),
		openapi.OptionWithServer(servers),
		openapi.OptionWithSecurity(security),
		openapi.OptionWithSourceComments(), // 注释即文档：字段/结构体注释进描述
		// 中间件文档钩子（RegisterMiddlewareDoc）无需 Option，进程级注册即生效
	); err != nil {
		panic(err)
	}
	fmt.Println("openapi spec written to", *out)
}
