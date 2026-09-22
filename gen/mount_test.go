package gen

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/hinge"
)

// ---- 测试基建：源码字符串 → Package ----

func parseTestPkg(t *testing.T, dir string, sources ...string) *Package {
	t.Helper()
	fset := token.NewFileSet()
	p := &Package{
		Dir:        dir,
		ImportPath: "example.com/app/" + dir,
		Name:       "app",
		aliases:    map[string]string{},
		methods:    map[string][]*ast.FuncDecl{},
	}
	for i, src := range sources {
		filename := dir + "_" + string(rune('a'+i)) + ".go"
		f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", filename, err)
		}
		p.Files = append(p.Files, collectFile(fset, filename, f))
		p.aggregate(f)
	}
	return p
}

// ---- 测试用业务源码（oapi:parent 注解形态）----
//
// mw "example.com/mw" 为假引用包：不在扫描包列表，verifyMWRef 跳过核对，
// 只验证引用解析与挂载链继承。

const mountSrc = `package app

import (
	"context"

	mw "example.com/mw"
)

// oapi:prefix /admin
// oapi:middleware mw.TraceMW
type AdminEp struct{}

// oapi:route GET
func (ep AdminEp) Index(ctx context.Context, _ any) (map[string]string, error) {
	return nil, nil
}

// oapi:parent AdminEp
// oapi:prefix /audit
// oapi:interceptor mw.AuthIC
type AuditEp struct{}

// oapi:route GET /events
func (ep AuditEp) ListEvents(ctx context.Context, q ListEventsQ) (map[string]any, error) {
	return nil, nil
}

// oapi:prefix /users
type UserEp struct{}

// oapi:route GET
func (ep UserEp) List(ctx context.Context, q ListQ) ([]string, error) {
	return nil, nil
}

type ListEventsQ struct {
	Page int ` + "`query:\"page\"`" + `
}

type ListQ struct {
	Page int ` + "`query:\"page\"`" + `
}
`

const chainSrc = `package app

import (
	"context"

	mw "example.com/mw"
)

// oapi:parent AuditEp
// oapi:prefix /export
// oapi:middleware mw.RateLimit
type ExportEp struct{}

// oapi:route GET
func (ep ExportEp) Dump(ctx context.Context, q ListEventsQ) (map[string]any, error) {
	return nil, nil
}
`

const cycleSrc = `package app

import "context"

// oapi:parent BEp
type AEp struct{}

// oapi:route GET
func (ep AEp) H(ctx context.Context, _ any) (string, error) { return "", nil }

// oapi:parent AEp
type BEp struct{}

// oapi:route GET
func (ep BEp) H(ctx context.Context, _ any) (string, error) { return "", nil }
`

// overlapSrc 方法级注解写全路径（与父链前缀重叠）的场景。
const overlapSrc = `package app

import "context"

// oapi:prefix /admin
type AdminEp struct{}

// oapi:route GET
func (ep AdminEp) Index(ctx context.Context, _ any) (map[string]string, error) {
	return nil, nil
}

// oapi:parent AdminEp
type AuditEp struct{}

// oapi:route GET /admin/audit
func (ep AuditEp) List(ctx context.Context, _ any) (map[string]any, error) {
	return nil, nil
}
`

// cfgMW / cfgIC Config 补充用具名函数（middlewareRef 反射判定形态）。
func cfgMW(next http.Handler) http.Handler { return next }

func cfgIC(ctx context.Context, ep hinge.Endpoint, r hinge.RequestReader, s hinge.Sink, next func(context.Context) error) error {
	return next(ctx)
}

func mustBuildIR(t *testing.T, cfg []EntryPointConfig) []*EndpointIR {
	t.Helper()
	eps, err := buildIR([]*Package{parseTestPkg(t, "app", mountSrc)}, cfg)
	if err != nil {
		t.Fatalf("buildIR: %v", err)
	}
	return eps
}

func findEp(t *testing.T, eps []*EndpointIR, owner, handler string) *EndpointIR {
	t.Helper()
	for _, ep := range eps {
		if ep.Owner == owner && ep.Handler == handler {
			return ep
		}
	}
	t.Fatalf("端点未找到：%s.%s", owner, handler)
	return nil
}

func wantErr(t *testing.T, cfg []EntryPointConfig, substrings ...string) {
	t.Helper()
	_, err := buildIR([]*Package{parseTestPkg(t, "app", mountSrc)}, cfg)
	if err == nil {
		t.Fatalf("期望生成期报错，实际通过")
	}
	for _, s := range substrings {
		if !strings.Contains(err.Error(), s) {
			t.Fatalf("错误信息缺少 %q:\n%s", s, err.Error())
		}
	}
}

func wantErrSrc(t *testing.T, src string, cfg []EntryPointConfig, substrings ...string) {
	t.Helper()
	_, err := buildIR([]*Package{parseTestPkg(t, "app", src)}, cfg)
	if err == nil {
		t.Fatalf("期望生成期报错，实际通过")
	}
	for _, s := range substrings {
		if !strings.Contains(err.Error(), s) {
			t.Fatalf("错误信息缺少 %q:\n%s", s, err.Error())
		}
	}
}

// ---- 用例 ----

// TestMountAnnotationFlat 注解挂载链主链路：路径沿祖先链拼接、
// struct 级中间件/拦截器沿链继承（先根后叶）进 Mount 字段。
func TestMountAnnotationFlat(t *testing.T) {
	eps := mustBuildIR(t, nil)

	cases := []struct {
		owner, handler, fullPath string
		mountMWs                 []string
		mountICs                 []string
	}{
		{"AdminEp", "Index", "/admin", nil, nil},
		// AuditEp 的 AuthIC 是自身注解拦截器 → 在 GroupICs（不在祖先链 MountICs）
		{"AuditEp", "ListEvents", "/admin/audit/events", []string{"TraceMW"}, nil},
		{"UserEp", "List", "/users", nil, nil},
	}
	for _, c := range cases {
		ep := findEp(t, eps, c.owner, c.handler)
		if ep.FullPath != c.fullPath {
			t.Errorf("%s.%s FullPath = %q, want %q", c.owner, c.handler, ep.FullPath, c.fullPath)
		}
		if len(ep.MountMWs) != len(c.mountMWs) {
			t.Errorf("%s.%s MountMWs = %v, want %v", c.owner, c.handler, ep.MountMWs, c.mountMWs)
		}
		for i, want := range c.mountMWs {
			if ep.MountMWs[i].Name != want {
				t.Errorf("%s.%s MountMWs[%d] = %q, want %q", c.owner, c.handler, i, ep.MountMWs[i].Name, want)
			}
		}
		if len(ep.MountICs) != len(c.mountICs) {
			t.Errorf("%s.%s MountICs = %v, want %v", c.owner, c.handler, ep.MountICs, c.mountICs)
		}
		if c.owner == "AuditEp" {
			if len(ep.GroupICs) != 1 || ep.GroupICs[0].Name != "AuthIC" {
				t.Errorf("AuditEp GroupICs = %v, want [AuthIC]（自身注解拦截器在 GroupICs）", ep.GroupICs)
			}
		}
		for i, want := range c.mountICs {
			if ep.MountICs[i].Name != want {
				t.Errorf("%s.%s MountICs[%d] = %q, want %q", c.owner, c.handler, i, ep.MountICs[i].Name, want)
			}
		}
	}
}

// TestMountChainThreeLevel 三级链：孙节点沿链继承祖父+父的中间件与拦截器，
// 顺序先根后叶；路径三层串联。
func TestMountChainThreeLevel(t *testing.T) {
	eps, err := buildIR([]*Package{parseTestPkg(t, "app", mountSrc, chainSrc)}, nil)
	if err != nil {
		t.Fatalf("buildIR: %v", err)
	}
	ep := findEp(t, eps, "ExportEp", "Dump")
	if ep.FullPath != "/admin/audit/export" {
		t.Errorf("Dump FullPath = %q, want /admin/audit/export", ep.FullPath)
	}
	// 链：AdminEp(TraceMW) → AuditEp(AuthIC)；ExportEp 自身的 RateLimit 在自身组级，不入 Mount
	if len(ep.MountMWs) != 1 || ep.MountMWs[0].Name != "TraceMW" {
		t.Errorf("MountMWs = %v, want [TraceMW]", ep.MountMWs)
	}
	if len(ep.MountICs) != 1 || ep.MountICs[0].Name != "AuthIC" {
		t.Errorf("MountICs = %v, want [AuthIC]", ep.MountICs)
	}
}

// TestMountLinearCompat 无 parent 注解的 ep 行为不变：Config 补充照常生效
// （per-ep，不沿链），未配置的 ep 保持注解原样。
func TestMountLinearCompat(t *testing.T) {
	eps := mustBuildIR(t, []EntryPointConfig{
		{Name: "UserEp", Middlewares: []any{cfgMW}},
	})
	// Config 补充（per-ep）
	if ep := findEp(t, eps, "UserEp", "List"); len(ep.RouteMWs) != 1 {
		t.Errorf("UserEp RouteMWs = %v, want 1 项", ep.RouteMWs)
	}
	// 未配置的 ep 不受影响
	if ep := findEp(t, eps, "AdminEp", "Index"); ep.FullPath != "/admin" || len(ep.RouteMWs) != 0 {
		t.Errorf("AdminEp Index = %q mws=%v, want /admin 且无补充", ep.FullPath, ep.RouteMWs)
	}
}

// TestMountConfigPerEP Config 补充不沿挂载链：父节点的 Interceptors 只作用于
// 自己，不传给子节点（链继承只属于注解）。
func TestMountConfigPerEP(t *testing.T) {
	eps := mustBuildIR(t, []EntryPointConfig{
		{Name: "AdminEp", Interceptors: []hinge.Interceptor{cfgIC}},
	})
	if ep := findEp(t, eps, "AdminEp", "Index"); len(ep.ConfigICs) != 1 {
		t.Errorf("AdminEp ConfigICs = %v, want 1 项", ep.ConfigICs)
	}
	if ep := findEp(t, eps, "AuditEp", "ListEvents"); len(ep.ConfigICs) != 0 {
		t.Errorf("AuditEp ConfigICs = %v, want 0（Config 补充不沿链）", ep.ConfigICs)
	}
}

// TestMountFuncDeclsLeaf FuncDecls 覆写仅作用于本节点端点。
func TestMountFuncDeclsLeaf(t *testing.T) {
	eps := mustBuildIR(t, []EntryPointConfig{
		{
			Name: "AuditEp",
			FuncDecls: map[FuncId]RouteMeta{
				"app.AuditEp.ListEvents": {Summary: "审计事件（覆写）"},
			},
		},
	})
	ep := findEp(t, eps, "AuditEp", "ListEvents")
	if ep.Summary != "审计事件（覆写）" {
		t.Errorf("ListEvents Summary = %q, want 覆写值", ep.Summary)
	}
}

// TestMountDiagnostics 生成期诊断：悬空 / 成环 / 自引用 / 前缀重叠。
func TestMountDiagnostics(t *testing.T) {
	t.Run("悬空", func(t *testing.T) {
		src := strings.Replace(mountSrc, "oapi:parent AdminEp", "oapi:parent NoSuchEp", 1)
		wantErrSrc(t, src, nil, "NoSuchEp", "未命中任何扫描到的 Enterpoint")
	})
	t.Run("成环", func(t *testing.T) {
		wantErrSrc(t, cycleSrc, nil, "成环")
	})
	t.Run("自引用", func(t *testing.T) {
		src := strings.Replace(mountSrc, "oapi:parent AdminEp", "oapi:parent AuditEp", 1)
		wantErrSrc(t, src, nil, "不能指向自身")
	})
	t.Run("前缀重叠", func(t *testing.T) {
		wantErrSrc(t, overlapSrc, nil, "已含挂载前缀", "相对挂载点声明")
	})
}
