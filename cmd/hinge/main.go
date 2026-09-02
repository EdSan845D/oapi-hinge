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
	"flag"
	"fmt"
	"os"

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
	if err := gen.Run(*dir, cfg, *check); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "hinge:", err)
	os.Exit(1)
}
