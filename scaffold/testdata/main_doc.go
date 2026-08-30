//go:build openapi

// 开发期文档生成入口：go run -tags openapi . -out openapi.yaml
// 仅在本构建中作为 OpenAPI 文档生成器使用；
// 本文件不参与 release 构建，运行时零开发期依赖。
package main

import (
	"flag"
	"fmt"
	"net/http"

	"github.com/EdSan845D/oapi-hinge/openapi"
	"opai-hinge/app/handlers"
	"opai-hinge/app/middleware"
	"opai-hinge/app/routes"

	"github.com/getkin/kin-openapi/openapi3"
)

func init() {
	// 中间件文档钩子（可选择性）：Auth 中间件所在组的所有 operation 标注 BearerAuth。
	// 401 响应由钩子按需声明（文档生成器不全局硬编码 401，公开接口不出现 401）。
	// 未在这里注册钩子的中间件照常运行，但不进文档。
	openapi.RegisterMiddlewareDoc(middleware.Auth, func(op *openapi3.Operation) {
		op.Security = &openapi3.SecurityRequirements{{"BearerAuth": []string{}}}
		op.Responses.Set("401", &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription("Unauthorized：token 缺失或无效")})
	})

	// 路由级纯文档增强（DescribeRoute）：错误响应声明 / OperationID 覆盖 / 响应头。
	// key = handler 函数引用（反射取「包.函数」）；只活在 doc 构建，release 二进制零内容。
	openapi.DescribeRoute(handlers.GetUser, openapi.RouteDoc{
		OperationID: "getUserById",
		Errors: []openapi.ErrorDecl{
			{Status: http.StatusNotFound, Description: "用户不存在"},
		},
	})
}

func main() {
	out := flag.String("out", "openapi.yaml", "openapi 文档输出路径（.yaml/.yml -> YAML，.json -> JSON）")
	flag.Parse()

	info := &openapi3.Info{
		Title:       "opai-hinge API",
		Version:     "1.0.0",
		Description: "hinge 仅作为开发期 OpenAPI 文档生成器；本规范由统一路由注册表自动生成（go run -tags openapi . -out openapi.yaml），请勿手改。",
	}
	servers := &openapi3.Servers{{URL: routes.BasePath}}
	security := openapi3.SecuritySchemes{
		"BearerAuth": &openapi3.SecuritySchemeRef{Value: openapi3.NewSecurityScheme().
			WithType("http").
			WithScheme("bearer").
			WithDescription("token 传递方式：Header `Authorization: Bearer <token>`")},
	}
	if err := openapi.Generate(
		*out,
		routes.All(),
		openapi.OptionWithDocInfo(info),
		openapi.OptionWithServer(servers),
		openapi.OptionWithSecurity(security),
		openapi.OptionWithSourceComments(), // 注释即文档：字段/结构体/handler 注释进描述
	); err != nil {
		panic(err)
	}
	fmt.Println("openapi spec written to", *out)
}
