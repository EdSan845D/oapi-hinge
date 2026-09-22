package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EdSan845D/oapi-hinge/example/app/eps"
	"github.com/EdSan845D/oapi-hinge/example/app/middleware"
	"github.com/EdSan845D/oapi-hinge/gen"
)

// EntryPointsConfig 程序化 Enterpoint 配置：gen.Run 同进程注入。
//   - Middlewares：组级框架原生中间件（运行时值，反射取名后发射为源码引用，组级直挂）；
//   - Interceptors：owner 全端点内核拦截器（反射取名后发射为 HandleWith extra 实参）；
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
	// 挂载关系不经 Config：oapi:parent 注解是唯一事实源（见 app/eps/admin.go，
	// AdminEp 组根 /admin + AuditEp oapi:parent AdminEp /audit）。
	return []gen.EntryPointConfig{
		{
			Name: "SystemEp",
			Middlewares: []any{
				middleware.Auth,
			},
			// Interceptors: []hinge.Interceptor{
			// 	middleware.AccessLog,
			// },
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
	tmplFilePath, err := filepath.Abs("./gin.row.tmpl")
	if err != nil {
		panic(err)
	}
	cfg := gen.Config{
		Module:  "github.com/EdSan845D/oapi-hinge/example",
		Scan:    []string{"./app/eps", "./app/middleware"},
		Out:     "./apigen",
		Targets: []string{"gin", "gin.row"}, // 内置 gin（内核管线）+ 自定义 row 模板（自足管线）双演示
		Emitters: map[string]gen.EmitConfig{
			"gin.row": {
				Title:    "GinRow",
				Template: tmplFilePath,
			},
		},
		// 程序化配置（含 Children 挂载树演示）：启用后产物头部带 entrypoints:
	// programmatic 标记，CLI gen/-check 拒绝执行（双入口产物漂移防护），
	// 生成与门禁都走本入口（go run ./app / go run ./app -check）。
	EntryPoints: EntryPointsConfig(),
	}
	if err := gen.Run(dir, cfg, *check); err != nil {
		fmt.Fprintln(os.Stderr, "hinge:", err)
		os.Exit(1)
	}
}
