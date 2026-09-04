package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
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
