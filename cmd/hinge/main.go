// hinge gen：oapi-hinge 代码生成 CLI。
//
// 用法（在业务模块根目录）：
//
//	go run github.com/EdSan845D/oapi-hinge/cmd/hinge gen              # 生成
//	go run github.com/EdSan845D/oapi-hinge/cmd/hinge gen -check      # CI 门禁：产物过期即失败
//
// 配置 hinge.gen.yaml：scan（Enterpoint 目录）/ out（生成包）/ targets（gin/echo/http）。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EdSan845D/oapi-hinge/gen"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "gen" {
		args = args[1:] // 兼容子命令形态：hinge gen [flags]
	}
	fs := flag.NewFlagSet("hinge", flag.ExitOnError)
	dir := fs.String("dir", ".", "模块根目录")
	config := fs.String("config", "hinge.gen.yaml", "配置文件（相对模块根）")
	check := fs.Bool("check", false, "校验生成产物是否最新（不写入；CI 门禁）")
	_ = fs.Parse(args)

	cfg, err := gen.LoadConfig(*dir, *config)
	if err != nil {
		fail(err)
	}
	// CLI 禁止程序化 EntryPoints 场景：产物头部带 entrypoints: programmatic 标记
	// 说明该仓库用 generate.go 同进程注入 EntryPointConfig（组级中间件/FuncDecls
	// 覆写），CLI 生成会丢失这些配置导致产物漂移。检测基于产物自描述，确定性。
	if err := rejectProgrammaticArtifacts(*dir, cfg); err != nil {
		fail(err)
	}
	if err := gen.Run(*dir, cfg, *check); err != nil {
		fail(err)
	}
}

// rejectProgrammaticArtifacts 检测产物头部标记：发现 entrypoints: programmatic
// 即拒绝 CLI 生成/-check，引导到项目内的程序化生成入口。无产物（首次生成）放行。
func rejectProgrammaticArtifacts(dir string, cfg gen.Config) error {
	specPath := filepath.Join(dir, filepath.FromSlash(cfg.Out), "specs_gen.go")
	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil // 无产物：首次生成，放行
	}
	if bytes.Contains(data, []byte("// entrypoints: programmatic")) {
		return fmt.Errorf("检测到程序化 EntryPoint 产物（%s 头部标记 entrypoints: programmatic）\n"+
			"  CLI 生成会丢失 EntryPointConfig（组级中间件 / FuncDecls 覆写），已被禁止。\n"+
			"  请使用项目内的程序化生成入口重新生成与 -check（generate.go 中 gen.Run 的调用方，\n"+
			"  参考 example/app/generate.go 的 -check flag）。", specPath)
	}
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "hinge:", err)
	os.Exit(1)
}
