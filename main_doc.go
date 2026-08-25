//go:build openapi

// 开发期文档生成入口：go run -tags openapi . -out openapi.yaml
// fuego 仅在本构建中作为 OpenAPI 文档生成器使用；
// 本文件与 internal/openapi 不参与 release 构建，运行时零开发期依赖。
package main

import (
	"flag"
	"fmt"

	"fuego-hinge/app/routes"
	"fuego-hinge/internal/openapi"
)

func main() {
	out := flag.String("out", "openapi.yaml", "openapi 文档输出路径（.yaml/.yml -> YAML，.json -> JSON）")
	flag.Parse()

	info := openapi.DocInfo{
		Title:       "fuego-hinge API",
		Version:     "1.0.0",
		Description: "fuego 仅作为开发期 OpenAPI 文档生成器；本规范由统一路由注册表自动生成（go run -tags openapi . -out openapi.yaml），请勿手改。",
	}
	if err := openapi.Generate(*out, routes.All(), info); err != nil {
		panic(err)
	}
	fmt.Println("openapi spec written to", *out)
}
