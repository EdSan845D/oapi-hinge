package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config hinge.gen.yaml 配置。
type Config struct {
	// Module 模块路径；缺省从 go.mod 读取。
	Module string `yaml:"module"`
	// Scan 扫描目录（相对模块根），Enteypoint 所在包。
	Scan []string `yaml:"scan"`
	// Out 生成包目录（相对模块根），如 apigen。
	Out string `yaml:"out"`
	// Pkg 生成包名；缺省取 Out 目录基名。
	Pkg string `yaml:"pkg"`
	// Targets 目标框架：gin / echo / http（可组合）。
	Targets []string `yaml:"targets"`
	// EntryPoints 程序化 Enterpoint 配置：generate.go 在 gen.Run 同进程注入的运行时值（不入 yaml）。
	// Midllwares 元素为具名包级函数（框架原生中间件 / hinge.Interceptor），
	// 生成器反射取名后发射为源码引用，运行时由各适配器 As*Chain 自动识别类型并挂载。
	EntryPoints []EntryPointConfig `yaml:"-"`
}

var moduleRe = regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)

// LoadConfig 读取配置；Module 缺省时从模块根的 go.mod 提取。
func LoadConfig(rootDir, path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(filepath.Join(rootDir, path))
	if err != nil {
		return cfg, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	if len(cfg.Scan) == 0 {
		return cfg, fmt.Errorf("配置缺少 scan（Enteypoint 所在目录）")
	}
	if cfg.Out == "" {
		return cfg, fmt.Errorf("配置缺少 out（生成包目录）")
	}
	if cfg.Module == "" {
		modData, err := os.ReadFile(filepath.Join(rootDir, "go.mod"))
		if err != nil {
			return cfg, fmt.Errorf("读取 go.mod: %w", err)
		}
		m := moduleRe.FindSubmatch(modData)
		if m == nil {
			return cfg, fmt.Errorf("go.mod 中未找到 module 声明")
		}
		cfg.Module = string(m[1])
	}
	if cfg.Pkg == "" {
		cfg.Pkg = filepath.Base(cfg.Out)
	}
	if len(cfg.Targets) == 0 {
		cfg.Targets = []string{"gin", "echo", "http"}
	}
	for _, t := range cfg.Targets {
		switch t {
		case "gin", "echo", "http":
		default:
			return cfg, fmt.Errorf("未知 target %q（支持 gin/echo/http）", t)
		}
	}
	return cfg, nil
}

type EntryPoint interface {
	EntryPointConfig() EntryPointConfig
}

type RouteMeta struct {
	Method            string
	Path              string
	Summary           string
	Description       string
	Tags              []string
	DefaultStatusCode int
	Deprecated        bool
	Envelope          string
}

type EntryPointConfig struct {
	Name       EntryId
	Prefix     string
	Tags       []string
	Midllwares []any
	FuncDecls  map[FuncId]RouteMeta
}

const PKGFlag = "PKG_"

// middlewareRef 把 Midllwares 元素（gen.Run 同进程的运行时值）解析为源码引用。
// 仅支持具名包级函数：gin.HandlerFunc / echo.MiddlewareFunc /
// func(http.Handler) http.Handler / hinge.Interceptor（本身即具名函数值）。
// 返回（限定名引用如 m.Auth, importPath）。
func middlewareRef(v any) (string, string, error) {
	if v == nil {
		return "nil", "", nil
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Func {
		return "", "", fmt.Errorf("元素类型 %T 不支持（需为具名包级函数）", v)
	}
	full := runtime.FuncForPC(rv.Pointer()).Name()
	full = strings.TrimSuffix(full, "-fm")
	if strings.Contains(full, ".(") || strings.Contains(full, "..") {
		return "", "", fmt.Errorf("方法值/闭包 %s 无法生成源码引用（请使用具名包级函数）", full)
	}
	dot := strings.LastIndex(full, ".")
	if dot <= 0 {
		return "", "", fmt.Errorf("函数名 %s 无法解析包路径", full)
	}
	pkgPath, fn := full[:dot], full[dot+1:]
	return path.Base(pkgPath) + "." + fn, pkgPath, nil
}

type EntryId string
type FuncId string

func FuncIdentity(fn any) FuncId {
	if fn == nil {
		return ""
	}
	v := reflect.ValueOf(fn)
	name := runtime.FuncForPC(v.Pointer()).Name()
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(name, "-fm")
	return FuncId(name)
}
