// oapi-hinge —— 生成 Go API 项目骨架的命令行工具（类似 pnpm create vite）。
//
// 用法：
//
//	oapi-hinge create myapp                    # 生成 ./myapp，module 名 = myapp
//	oapi-hinge create myapp -m github.com/me/myapp
//	oapi-hinge create myapp --no-tidy          # 跳过 go mod tidy
//	oapi-hinge create myapp --force            # 覆盖已存在的目录
//
// 生成产物：统一 Handler 模板 + 单一路由注册表 + 原生 Gin 运行时 + 文档生成（构建期隔离）。
// 内置示例业务（用户 CRUD + 文件下载 + 健康检查），删掉 app/handlers 下的示例即可开始写自己的业务。
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed testdata
var templateFS embed.FS

const templateRoot = "testdata"

func main() {
	fs := flag.NewFlagSet("oapi-hinge", flag.ExitOnError)
	module := fs.String("m", "", "Go module path（默认取项目名）")
	noTidy := fs.Bool("no-tidy", false, "跳过 go mod tidy")
	force := fs.Bool("force", false, "覆盖已存在的目录")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "oapi-hinge —— 生成 Go API 项目骨架（统一 Handler + 原生 Gin 运行时 + kin-openapi 开发期文档生成，release 零开发依赖）")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "用法:")
		fmt.Fprintln(os.Stderr, "  oapi-hinge create <project> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "flags:")
		fs.PrintDefaults()
	}

	// 兼容两种调用形式：oapi-hinge create <project> 与 oapi-hinge <project>
	// 并支持 flags 位于位置参数之后（Go flag 在首个非 flag 参数处停止解析，需先重排）
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "create" {
		args = args[1:]
	}
	args = reorderArgs(args)
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(1)
	}

	project := fs.Arg(0)
	if err := validateProjectName(project); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
	mod := *module
	if mod == "" {
		mod = project
	}
	if err := validateModuleName(mod); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}

	if err := scaffold(project, mod, *force); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}

	if !*noTidy {
		fmt.Println("运行 go mod tidy（首次会拉取依赖，可能较慢）...")
		if err := runTidy(project); err != nil {
			fmt.Fprintln(os.Stderr, "警告: go mod tidy 失败:", err)
			fmt.Fprintln(os.Stderr, "      可稍后在项目目录手动执行: go mod tidy")
		}
	}

	printNextSteps(project, mod, envVarName(mod), *noTidy)
}

// reorderArgs 把位置参数之后的 flags 重排到前面（Go flag 在首个非 flag 参数处停止解析）
// 支持：-m value（空格分隔）、-m=value、--no-tidy 等
func reorderArgs(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			// 空格分隔取值的 flag：-m <value> / --module <value>
			if (a == "-m" || a == "--module") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func validateProjectName(name string) error {
	if name == "" {
		return fmt.Errorf("项目名不能为空")
	}
	if name == "." || strings.ContainsAny(name, `/\:`) {
		return fmt.Errorf("项目名不能包含路径分隔符: %s", name)
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("项目名不能以点开头: %s", name)
	}
	return nil
}

func validateModuleName(mod string) error {
	if mod == "" {
		return fmt.Errorf("module 名不能为空")
	}
	if strings.ContainsAny(mod, " \t") {
		return fmt.Errorf("module 名不能包含空白字符: %s", mod)
	}
	return nil
}

// scaffold 把内置模板写入目标目录，并把占位 module 名替换为实际值
func scaffold(project, mod string, force bool) error {
	if _, err := os.Stat(project); err == nil {
		if !force {
			return fmt.Errorf("目录已存在: %s（如需覆盖请加 --force）", project)
		}
		fmt.Printf("目录 %s 已存在，--force 模式继续覆盖...\n", project)
	} else if !os.IsNotExist(err) {
		return err
	}

	envName := envVarName(mod)

	return fs.WalkDir(templateFS, templateRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(path, templateRoot+"/")
		// v0.2 起模板已迁移到 Enterpoint 范式；v0.1 残留模板不再拷出（待后续版本从 embed 中移除）
		for _, legacy := range []string{"app/handlers/", "app/routes/", "app/middleware/"} {
			if strings.HasPrefix(rel, legacy) {
				return nil
			}
		}
		// 模板中的 go.mod.tmpl 在生成时改名为 go.mod
		if rel == "go.mod.tmpl" {
			rel = "go.mod"
		}
		out := filepath.Join(project, rel)

		data, err := templateFS.ReadFile(path)
		if err != nil {
			return err
		}
		content := strings.ReplaceAll(string(data), "opai-hinge", mod)
		content = strings.ReplaceAll(content, "OAPI_HINGE", envName)

		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
			return err
		}
		fmt.Printf("  created %s\n", filepath.Join(project, rel))
		return nil
	})
}

// envVarName 从 module 名推导环境变量前缀：github.com/me/my-app -> MY_APP
func envVarName(mod string) string {
	base := mod
	if i := strings.LastIndex(mod, "/"); i >= 0 {
		base = mod[i+1:]
	}
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 32)
		case r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func runTidy(dir string) error {
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func printNextSteps(project, mod, env string, noTidy bool) {
	fmt.Println()
	fmt.Println("✅ 项目已生成:", project)
	fmt.Println()
	fmt.Println("下一步:")
	fmt.Printf("  cd %s\n", project)
	fmt.Printf("  %s=dev go run .                          # 启动（dev 模式跳过示例鉴权）\n", env)
	fmt.Println("  go run -tags openapi . -out openapi.yaml   # 生成 OpenAPI 文档")
	fmt.Println("  ./build.sh -r                          # release 构建")
	fmt.Println()
	fmt.Println("module:", mod)
	fmt.Println()
	fmt.Println("新增接口三步: 1) app/eps 写端点方法 + oapi:* 注解  2) go run github.com/EdSan845D/oapi-hinge/cmd/hinge gen  3) 完成（注册与文档表已生成）")
}
