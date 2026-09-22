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

// ---- 具名包级函数（middlewareRef 反射取名的对象）----

// TraceMW 框架原生中间件形态（http 适配器风格）。
func TraceMW(next http.Handler) http.Handler { return next }

// AuthIC 内核拦截器形态（hinge.Interceptor：5 参 1 返）。
func AuthIC(ctx context.Context, ep hinge.Endpoint, r hinge.RequestReader, s hinge.Sink, next func(context.Context) error) error {
	return next(ctx)
}

// AuditIC 第二个内核拦截器（顺序断言用）。
func AuditIC(ctx context.Context, ep hinge.Endpoint, r hinge.RequestReader, s hinge.Sink, next func(context.Context) error) error {
	return next(ctx)
}

// ---- 测试用业务源码 ----

// mountSrc 三个 Enterpoint：ApiV1Ep（组根，纯挂载层）、UserEp（注解固有前缀）、OrderEp（挂载点前缀）。
const mountSrc = `package app

import "context"

type ApiV1Ep struct{}

// oapi:route GET
func (ep ApiV1Ep) Index(ctx context.Context, _ any) (map[string]string, error) {
	return nil, nil
}

// oapi:prefix /users
type UserEp struct{}

// oapi:route GET
func (ep UserEp) List(ctx context.Context, q ListUsersQ) ([]User, error) {
	return nil, nil
}

// oapi:route GET /{id}
func (ep UserEp) Get(ctx context.Context, q GetUserQ) (User, error) {
	return User{}, nil
}

type OrderEp struct{}

// oapi:route GET
func (ep OrderEp) List(ctx context.Context, q ListOrdersQ) (map[string]any, error) {
	return nil, nil
}

type ListUsersQ struct {
	Page int ` + "`query:\"page\"`" + `
}

type GetUserQ struct {
	ID string ` + "`path:\"id\"`" + `
}

type ListOrdersQ struct {
	Status string ` + "`query:\"status\"`" + `
}

type User struct {
	ID   string ` + "`json:\"id\"`" + `
	Name string ` + "`json:\"name\"`" + `
}
`

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

// overlapSrc 方法级注解写全路径（与挂载点重叠）的场景。
const overlapSrc = `package app

import "context"

type UserEp struct{}

// oapi:route GET /users/list
func (ep UserEp) List(ctx context.Context, q ListUsersQ) ([]User, error) {
	return nil, nil
}

type ListUsersQ struct {
	Page int ` + "`query:\"page\"`" + `
}

type User struct {
	ID string ` + "`json:\"id\"`" + `
}
`

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

// TestMountTreeFlat 树展平主链路：路径沿祖先链拼接、中间件/拦截器沿树继承。
func TestMountTreeFlat(t *testing.T) {
	eps := mustBuildIR(t, []EntryPointConfig{
		{
			Name: "ApiV1Ep", Prefix: "/api/v1", Middlewares: []any{TraceMW},
			Children: []EntryPointConfig{
				{Name: "UserEp", Interceptors: []hinge.Interceptor{AuthIC}},
				{Name: "OrderEp", Prefix: "/orders"},
			},
		},
	})

	cases := []struct {
		owner, handler, fullPath string
		mws                      []string
		ics                      []string
	}{
		{"ApiV1Ep", "Index", "/api/v1", []string{"gen.TraceMW"}, nil},
		{"UserEp", "List", "/api/v1/users", []string{"gen.TraceMW"}, []string{"gen.AuthIC"}},
		{"UserEp", "Get", "/api/v1/users/{id}", []string{"gen.TraceMW"}, []string{"gen.AuthIC"}},
		{"OrderEp", "List", "/api/v1/orders", []string{"gen.TraceMW"}, nil},
	}
	for _, c := range cases {
		ep := findEp(t, eps, c.owner, c.handler)
		if ep.FullPath != c.fullPath {
			t.Errorf("%s.%s FullPath = %q, want %q", c.owner, c.handler, ep.FullPath, c.fullPath)
		}
		if len(ep.RouteMWs) != len(c.mws) {
			t.Errorf("%s.%s RouteMWs = %v, want %v", c.owner, c.handler, ep.RouteMWs, c.mws)
		}
		for i, want := range c.mws {
			if ep.RouteMWs[i].Ref != want {
				t.Errorf("%s.%s RouteMWs[%d] = %q, want %q", c.owner, c.handler, i, ep.RouteMWs[i].Ref, want)
			}
		}
		if len(ep.ConfigICs) != len(c.ics) {
			t.Errorf("%s.%s ConfigICs = %v, want %v", c.owner, c.handler, ep.ConfigICs, c.ics)
		}
		for i, want := range c.ics {
			if ep.ConfigICs[i].Name != strings.SplitN(want, ".", 2)[1] {
				t.Errorf("%s.%s ConfigICs[%d] = %q, want %q", c.owner, c.handler, i, ep.ConfigICs[i].Name, want)
			}
		}
	}
}

// TestMountInterceptorOrder 拦截器沿树先根后叶：祖先 Config ICs 先于叶节点、
// 叶节点 Config ICs 先于结构体/方法级注解（结构体级注解已含于 UserEp 之外的源码，此处只验 Config 链）。
func TestMountInterceptorOrder(t *testing.T) {
	eps := mustBuildIR(t, []EntryPointConfig{
		{
			Name: "ApiV1Ep", Prefix: "/api/v1", Interceptors: []hinge.Interceptor{AuditIC},
			Children: []EntryPointConfig{
				{Name: "UserEp", Interceptors: []hinge.Interceptor{AuthIC}},
			},
		},
	})
	ep := findEp(t, eps, "UserEp", "List")
	if len(ep.ConfigICs) != 2 || ep.ConfigICs[0].Name != "AuditIC" || ep.ConfigICs[1].Name != "AuthIC" {
		t.Fatalf("ConfigICs 顺序错误 = %v，want [AuditIC AuthIC]", ep.ConfigICs)
	}
}

// TestMountLinearCompat 无 Children 的线性用法行为不变：挂载点作用于组根、
// 未挂载的 Enterpoint 保持注解原样（v0.2.1 既有形态回归）。
func TestMountLinearCompat(t *testing.T) {
	eps := mustBuildIR(t, []EntryPointConfig{
		{Name: "OrderEp", Prefix: "/orders", Middlewares: []any{TraceMW}},
	})
	// 挂载点作用于组根：FullPath=/ → /orders
	if ep := findEp(t, eps, "OrderEp", "List"); ep.FullPath != "/orders" {
		t.Errorf("List FullPath = %q, want /orders", ep.FullPath)
	}
	// 未挂载的 Enterpoint 不受影响
	if ep := findEp(t, eps, "UserEp", "List"); ep.FullPath != "/users" {
		t.Errorf("List FullPath = %q, want /users", ep.FullPath)
	}
	if ep := findEp(t, eps, "UserEp", "Get"); ep.FullPath != "/users/{id}" {
		t.Errorf("Get FullPath = %q, want /users/{id}", ep.FullPath)
	}
	if ep := findEp(t, eps, "ApiV1Ep", "Index"); ep.FullPath != "/" {
		t.Errorf("Index FullPath = %q, want /", ep.FullPath)
	}
}

// TestMountFuncDeclsLeaf 叶节点 FuncDecls 覆写仅作用于本节点端点。
func TestMountFuncDeclsLeaf(t *testing.T) {
	eps := mustBuildIR(t, []EntryPointConfig{
		{
			Name: "ApiV1Ep", Prefix: "/api/v1",
			Children: []EntryPointConfig{
				{Name: "UserEp", FuncDecls: map[FuncId]RouteMeta{
					"app.UserEp.List": {Summary: "用户列表（覆写）"},
				}},
			},
		},
	})
	ep := findEp(t, eps, "UserEp", "List")
	if ep.Summary != "用户列表（覆写）" {
		t.Errorf("List Summary = %q, want 覆写值", ep.Summary)
	}
}

// TestMountDiagnostics 生成期诊断：Name 未命中 / 同 owner 多挂载 / 注解路径已含挂载点。
func TestMountDiagnostics(t *testing.T) {
	t.Run("Name未命中", func(t *testing.T) {
		wantErr(t, []EntryPointConfig{{Name: "NoSuchEp", Prefix: "/x"}},
			"NoSuchEp", "未命中任何扫描到的 Enterpoint")
	})
	t.Run("同owner多挂载", func(t *testing.T) {
		wantErr(t, []EntryPointConfig{
			{Name: "UserEp", Prefix: "/a"},
			{Name: "ApiV1Ep", Prefix: "/api", Children: []EntryPointConfig{
				{Name: "UserEp"},
			}},
		}, "UserEp", "挂载多处")
	})
	t.Run("注解路径已含挂载点", func(t *testing.T) {
		// 方法级注解写了从根开始的全路径 /users/list，再挂到 /users 下 → 重叠报错
		wantErrSrc(t, overlapSrc, []EntryPointConfig{
			{Name: "UserEp", Prefix: "/users"},
		}, "/users/list", "已含挂载点前缀", "相对挂载点声明")
	})
}
