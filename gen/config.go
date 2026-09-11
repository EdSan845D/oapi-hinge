package gen

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"

	"github.com/EdSan845D/oapi-hinge/hinge"

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
	// Targets 目标框架中的代码生成配置, 根据Target名称匹配
	Emitters map[string]EmitConfig `yaml:"emiters"`
	// EntryPoints 程序化 Enterpoint 配置：generate.go 在 gen.Run 同进程注入的运行时值（不入 yaml）。
	// Midllwares 元素为具名包级函数（框架原生中间件 / hinge.Interceptor），
	// 生成器反射取名后发射为源码引用，运行时由各适配器 As*Chain 自动识别类型并挂载。
	EntryPoints []EntryPointConfig `yaml:"-"`
}

// 代码生成时的配置
type EmitConfig struct {
	Title string `yaml:"title"`
	// PathStyle 路径参数风格：colon（:id，gin/echo）| brace（{id}，http/chi）。空 = colon。
	PathStyle string `yaml:"path_style"`
	// Template 注册文件模板：空 → 内置（templates/<target>.tmpl，仅 gin/echo/http）；
	// 非空 → 模板文件路径（相对模块根或绝对路径），新框架在此接入。
	Template string `yaml:"template"`
}

func (c Config) GetEmiter(target string) EmitConfig {
	if c.Emitters != nil {
		emiter, ok := c.Emitters[target]
		if ok {
			return emiter
		}
	}
	emiter, ok := map[string]EmitConfig{
		"gin": {
			Title: "Gin",
		},
		"echo": {
			Title: "Echo",
		},
		"http": {
			Title:     "HTTP",
			PathStyle: "brace",
		},
	}[target]
	if ok {
		return emiter
	}
	panic(fmt.Sprintf("target [%s] no matched emiter config, pls check the config field `emiters`", target))
}

func (c *Config) InitDefault() {
	if c.Module == "" {
		path, err := os.Executable()
		modData, err := os.ReadFile(filepath.Join(filepath.Dir(path), "go.mod"))
		if err != nil {
			panic(fmt.Errorf("读取 go.mod: %w", err))
		}
		m := moduleRe.FindSubmatch(modData)
		if m == nil {
			panic(fmt.Errorf("go.mod 中未找到 module 声明"))
		}
		c.Module = string(m[1])
	}
	if c.Pkg == "" {
		c.Pkg = filepath.Base(filepath.FromSlash(c.Out))
	}
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
		return cfg, fmt.Errorf("至少选择一种框架和匹配的模板，内置有gin,echo,http")
	}
	return cfg, nil
}

type EntryPoint interface {
	EntryPointConfig() EntryPointConfig
}

// Ptr 返回 v 的指针：RouteMeta.Deprecated 三态覆写用
// （nil = 不覆盖 / Ptr(true) = 置位 / Ptr(false) = 清除）。
func Ptr[T any](v T) *T { return new(v) }

type RouteMeta struct {
	// Method / Path 程序化路由覆写：预留字段，暂未消费——路由以注解为唯一事实源。
	Method string
	Path   string
	// 以下为字段级覆写（FuncDecls 命中端点后，非零值才覆盖注解值，零值保持注解不变）：
	Summary           string
	Description       string
	Tags              []string
	DefaultStatusCode int
	Envelope          string
	// Deprecated 三态覆写：nil = 不覆盖；Ptr(true) = 置弃用标记；Ptr(false) = 清除
	//（注解 oapi:deprecated 只能置位无法撤销）。
	Deprecated *bool
}

type EntryPointConfig struct {
	Name       EntryId
	Prefix     string
	Tags       []string
	Midllwares []any
	// FuncDecls 字段级程序化覆写：键为 FuncIdentity(fn) 派生的函数标识
	//（如 "eps.SystemEp.Health"），值为按字段合并的覆写元数据（非零字段才生效）。
	// 命中的端点在生成期输出覆写提示，保证代码定义的覆写可见。
	FuncDecls map[FuncId]RouteMeta
}

const PKGFlag = "PKG_"

// middlewareRef 把 Midllwares 元素（gen.Run 同进程的运行时值）解析为源码引用。
// 仅支持具名包级函数：gin.HandlerFunc / echo.MiddlewareFunc /
// func(http.Handler) http.Handler / hinge.Interceptor（本身即具名函数值）。
// 返回（限定名引用如 m.Auth, importPath, 是否 hinge.Interceptor）。
// gen.Run 与调用方同进程，运行时类型可精确判定，无需源码级签名分析。
func middlewareRef(v any) (string, string, bool, error) {
	if v == nil {
		return "nil", "", false, nil
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Func {
		return "", "", false, fmt.Errorf("元素类型 %T 不支持（需为具名包级函数）", v)
	}
	full := runtime.FuncForPC(rv.Pointer()).Name()
	full = strings.TrimSuffix(full, "-fm")
	if strings.Contains(full, ".(") || strings.Contains(full, "..") {
		return "", "", false, fmt.Errorf("方法值/闭包 %s 无法生成源码引用（请使用具名包级函数）", full)
	}
	dot := strings.LastIndex(full, ".")
	if dot <= 0 {
		return "", "", false, fmt.Errorf("函数名 %s 无法解析包路径", full)
	}
	pkgPath, fn := full[:dot], full[dot+1:]
	// 可赋值给 hinge.Interceptor（定义的函数类型）：需要端点上下文，
	// 发射为每路由显式包装；其余按框架原生中间件处理。
	interceptor := rv.Type().AssignableTo(reflect.TypeOf(hinge.Interceptor(nil)))
	return path.Base(pkgPath) + "." + fn, pkgPath, interceptor, nil
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
