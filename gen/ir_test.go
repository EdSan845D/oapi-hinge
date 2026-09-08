package gen

import (
	"go/ast"
	"testing"
)

func TestJoinRoutePath(t *testing.T) {
	cases := []struct{ prefix, rel, want string }{
		{"/users", "", "/users"},
		{"/users", "/", "/users"},
		{"/users", "/{id}", "/users/{id}"},
		{"/users", "{id}", "/users/{id}"},
		{"", "/health", "/health"},
		{"", "", "/"},
		{"/users/", "", "/users"},
		{"/a", "/b/c", "/a/b/c"},
	}
	for _, c := range cases {
		if got := joinRoutePath(c.prefix, c.rel); got != c.want {
			t.Errorf("joinRoutePath(%q, %q) = %q, want %q", c.prefix, c.rel, got, c.want)
		}
	}
}

func TestAnnotationsParsing(t *testing.T) {
	doc := &ast.CommentGroup{List: []*ast.Comment{
		{Text: "// oapi:route GET /users"},
		{Text: "// 用户列表（分页）"},
		{Text: "// oapi:status 201"},
		{Text: "// 第二行描述"},
	}}
	kv, lines := annotations(doc)
	if len(kv) != 2 || kv[0][0] != "route" || kv[0][1] != "GET /users" || kv[1][0] != "status" || kv[1][1] != "201" {
		t.Fatalf("kv mismatch: %v", kv)
	}
	if len(lines) != 2 || lines[0] != "用户列表（分页）" || lines[1] != "第二行描述" {
		t.Fatalf("doc lines mismatch: %v", lines)
	}
}

func TestPathParamRegex(t *testing.T) {
	if got := pathParamRe.FindAllStringSubmatch("/users/{id}/files/{name}", -1); len(got) != 2 || got[0][1] != "id" || got[1][1] != "name" {
		t.Fatalf("path params mismatch: %v", got)
	}
}

func TestSplitMWRefs(t *testing.T) {
	pkg := &Package{Files: []*File{{
		byAlias: map[string]string{"mw": "example.com/app/mw"},
		byBase:  map[string]string{"middleware": "example.com/app/middleware"},
	}}}
	scanned := map[string]string{
		"middleware": "example.com/app/middleware", // 被扫描包名 → 可解析
		"dup":        "",                           // 同名多包歧义 → 不解析
	}
	kernel, refs := splitMWRefs(pkg, scanned, []string{
		"mw.ParseHeader",                 // 端点包显式别名 import → 路由级
		"middleware.ParseHeaderWithInfo", // 被扫描包名 → 路由级
		"github.com/x/lib/mw.Start",      // 完整 import 路径 → 路由级（别名取基名）
		"Auth",                           // 无点 → 内核拦截器注册名
		"registry.Named",                 // dotted 但限定符未解析 → 内核注册名
		"dup.Fn",                         // 歧义限定符 → 内核注册名
	})
	wantRefs := []MWRef{
		{Qualifier: "mw", Name: "ParseHeader", Import: "example.com/app/mw"},
		{Qualifier: "middleware", Name: "ParseHeaderWithInfo", Import: "example.com/app/middleware"},
		{Qualifier: "mw", Name: "Start", Import: "github.com/x/lib/mw"},
	}
	if len(refs) != len(wantRefs) {
		t.Fatalf("refs = %+v, want %+v", refs, wantRefs)
	}
	for i := range wantRefs {
		if refs[i] != wantRefs[i] {
			t.Fatalf("refs[%d] = %+v, want %+v", i, refs[i], wantRefs[i])
		}
	}
	wantKernel := []string{"Auth", "registry.Named", "dup.Fn"}
	if len(kernel) != len(wantKernel) {
		t.Fatalf("kernel names = %v, want %v", kernel, wantKernel)
	}
	for i := range wantKernel {
		if kernel[i] != wantKernel[i] {
			t.Fatalf("kernel names = %v, want %v", kernel, wantKernel)
		}
	}
	// 空名单不产生引用
	if k2, r2 := splitMWRefs(pkg, scanned, nil); len(k2) != 0 || len(r2) != 0 {
		t.Fatalf("nil input: kernel=%v refs=%v", k2, r2)
	}
}

func TestScanQualifiers(t *testing.T) {
	pkgs := []*Package{
		{Name: "eps", ImportPath: "example.com/app/eps"},
		{Name: "middleware", ImportPath: "example.com/app/middleware"},
		{Name: "middleware", ImportPath: "example.com/other/middleware"}, // 同名多包 → 歧义
	}
	m := scanQualifiers(pkgs)
	if m["eps"] != "example.com/app/eps" {
		t.Fatalf("eps qualifier mismatch: %v", m)
	}
	if m["middleware"] != "" {
		t.Fatalf("ambiguous qualifier should be empty: %v", m)
	}
}
