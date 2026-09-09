package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/EdSan845D/oapi-hinge/example/app/eps"
	"github.com/EdSan845D/oapi-hinge/example/app/middleware"
	"github.com/EdSan845D/oapi-hinge/gen"
)

// EntryPointsConfig 程序化 Enterpoint 配置：gen.Run 同进程注入。
//   - Midllwares：组级中间件（运行时值，反射取名后发射为源码引用）；
//   - FuncDecls：字段级文档元数据覆写——键 = FuncIdentity(fn)，值按字段合并
//     （非零字段才覆盖注解值；Deprecated 为 *bool 三态），命中端点在生成期
//     输出覆写提示。
//
// 本仓库使用程序化 EntryPoint 配置：产物头部带 entrypoints: programmatic 标记，
// CLI gen/-check 会拒绝执行（双入口产物漂移防护），生成与门禁都走本入口：
//
//	go run ./app            # 生成
//	go run ./app -check     # CI 门禁：产物过期即失败
func EntryPointsConfig() []gen.EntryPointConfig {
	return []gen.EntryPointConfig{
		{
			Name: "SystemEp",
			Midllwares: []any{
				middleware.Auth,
			},
			FuncDecls: map[gen.FuncId]gen.RouteMeta{

				gen.FuncIdentity(eps.SystemEp.Health): {
					// 代码覆写注解：summary/description 以代码为准，deprecated 置位
					Summary:     "健康检查（代码覆写示例）",
					Description: "描述由 EntryPointConfig.FuncDecls 程序化覆写：非零字段覆盖注解值，生成日志会打印覆写提示。",
					Deprecated:  gen.Ptr(true),
				},
			},
		},
	}
}

func main() {
	check := flag.Bool("check", false, "校验生成产物是否最新（CI 门禁；不写入）")
	flag.Parse()
	dir := ""
	cfg := gen.Config{
		Module:      "github.com/EdSan845D/oapi-hinge/example",
		Scan:        []string{"./app/eps", "./app/middleware"},
		Out:         "./apigen",
		Targets:     []string{"gin"},
		EntryPoints: EntryPointsConfig(),
	}
	if err := gen.Run(dir, cfg, *check); err != nil {
		fmt.Fprintln(os.Stderr, "hinge:", err)
		os.Exit(1)
	}
}
