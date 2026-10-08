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

func TestResolveAnnoRefs(t *testing.T) {
	pkg := &Package{Files: []*File{{
		byAlias: map[string]string{"mw": "example.com/app/mw"},
		byBase:  map[string]string{"middleware": "example.com/app/middleware"},
	}}}
	scanned := map[string]string{
		"middleware": "example.com/app/middleware", // 被扫描包名 → 可解析
		"dup":        "",                           // 同名多包歧义 → 不解析
	}
	b := &irBuilder{}
	refs := b.resolveAnnoRefs(pkg, scanned, []string{
		"mw.ParseHeader",                 // 端点包显式别名 import → 引用
		"middleware.ParseHeaderWithInfo", // 被扫描包名 → 引用
		"github.com/x/lib/mw.Start",      // 完整 import 路径 → 引用（别名取基名）
		"Auth",                           // 无点 → 无法解析
		"registry.Named",                 // dotted 但限定符未解析 → 无法解析
		"dup.Fn",                         // 歧义限定符 → 无法解析
	}, "oapi:middleware", "T.Ping")
	want := []MWRef{
		{Qualifier: "mw", Name: "ParseHeader", Import: "example.com/app/mw"},
		{Qualifier: "middleware", Name: "ParseHeaderWithInfo", Import: "example.com/app/middleware"},
		{Qualifier: "mw", Name: "Start", Import: "github.com/x/lib/mw"},
	}
	if len(refs) != len(want) {
		t.Fatalf("refs = %+v, want %+v", refs, want)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Fatalf("refs[%d] = %+v, want %+v", i, refs[i], want[i])
		}
	}
	// 无法解析的值必须生成错误：注册表已移除，不存在裸名回落（防静默丢失）
	if len(b.errs) != 3 {
		t.Fatalf("errs = %v, want 3 项解析失败", b.errs)
	}
	// 空名单不产生引用与错误
	b2 := &irBuilder{}
	if r2 := b2.resolveAnnoRefs(pkg, scanned, nil, "oapi:middleware", "T.Ping"); len(r2) != 0 || len(b2.errs) != 0 {
		t.Fatalf("nil input: refs=%v errs=%v", r2, b2.errs)
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

// ---- FuncDecls 字段级覆写：applyRouteMeta / funcIdOf ----

func TestApplyRouteMeta(t *testing.T) {
	ep := &EndpointIR{
		Summary:    "注解摘要",
		Tags:       []string{"注解tag"},
		Status:     200,
		Envelope:   "env1",
		Deprecated: true,
	}
	// 零值字段跳过；显式字段覆盖；Deprecated 三态（清除）
	changed := applyRouteMeta(ep, RouteMeta{
		Summary:           "代码摘要",
		DefaultStatusCode: 201,
		Deprecated:        Ptr(false),
	})
	if ep.Summary != "代码摘要" || ep.Status != 201 || ep.Deprecated {
		t.Fatalf("override mismatch: %+v", ep)
	}
	if ep.Description != "" || len(ep.Tags) != 1 || ep.Envelope != "env1" {
		t.Fatalf("zero-value fields must keep annotation values: %+v", ep)
	}
	if len(changed) != 3 || changed[0] != "summary" || changed[1] != "status=201" || changed[2] != "deprecated=false" {
		t.Fatalf("changed list mismatch: %v", changed)
	}
	// Deprecated 置位
	applyRouteMeta(ep, RouteMeta{Deprecated: Ptr(true)})
	if !ep.Deprecated {
		t.Fatal("deprecated should be set")
	}
}

func TestFuncIdOf(t *testing.T) {
	ep := &EndpointIR{Pkg: &Package{Name: "eps"}, Owner: "UserEp", Handler: "DeleteUser"}
	if got := funcIdOf(ep); got != "eps.UserEp.DeleteUser" {
		t.Fatalf("funcIdOf = %q", got)
	}
	pkgEp := &EndpointIR{Pkg: &Package{Name: "eps"}, Owner: "PKG_eps", Handler: "Index"}
	if got := funcIdOf(pkgEp); got != "eps.Index" {
		t.Fatalf("pkg funcIdOf = %q", got)
	}
}

// ---- oapi:intercepter 拼写手误：fail fast 且提示更正，不误报为"已移除" ----

const typoMethodAnnoSrc = `package app

import "context"

// oapi:prefix /t
type TypoEp struct{}

// oapi:route GET
// oapi:intercepter mw.Auth
func (ep TypoEp) Ping(ctx context.Context, _ any) (string, error) {
	return "", nil
}
`

const typoStructAnnoSrc = `package app

import "context"

// oapi:prefix /t
// oapi:intercepter mw.Auth
type TypoEp struct{}

// oapi:route GET
func (ep TypoEp) Ping(ctx context.Context, _ any) (string, error) {
	return "", nil
}
`

func TestMethodAnnotationInterceptorTypo(t *testing.T) {
	wantErrSrc(t, typoMethodAnnoSrc, nil, "intercepter 拼写有误", "oapi:interceptor")
}

func TestStructAnnotationInterceptorTypo(t *testing.T) {
	wantErrSrc(t, typoStructAnnoSrc, nil, "intercepter 拼写有误", "oapi:interceptor")
}

// ---- oapi:deprecated 单向标记：只接受空值或 true，其余值生成期报错 ----

const deprecatedOkSrc = `package app

import "context"

// oapi:prefix /d
type DepEp struct{}

// oapi:route GET /a
// oapi:deprecated
func (ep DepEp) A(ctx context.Context, _ any) (string, error) { return "", nil }

// oapi:route GET /b
// oapi:deprecated true
func (ep DepEp) B(ctx context.Context, _ any) (string, error) { return "", nil }
`

const deprecatedFalseSrc = `package app

import "context"

// oapi:prefix /d
type DepEp struct{}

// oapi:route GET /a
// oapi:deprecated false
func (ep DepEp) A(ctx context.Context, _ any) (string, error) { return "", nil }
`

const deprecatedGarbageSrc = `package app

import "context"

// oapi:prefix /d
type DepEp struct{}

// oapi:route GET /a
// oapi:deprecated 0
func (ep DepEp) A(ctx context.Context, _ any) (string, error) { return "", nil }
`

func TestDeprecatedAnnotationNoValueOrTrue(t *testing.T) {
	eps, err := buildIR([]*Package{parseTestPkg(t, "app", deprecatedOkSrc)}, nil)
	if err != nil {
		t.Fatalf("buildIR: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("eps = %d, want 2", len(eps))
	}
	for _, ep := range eps {
		if !ep.Deprecated {
			t.Fatalf("%s.%s 应被标记弃用", ep.Owner, ep.Handler)
		}
	}
}

func TestDeprecatedAnnotationRejectsFalse(t *testing.T) {
	// 此前 false 被静默忽略并仍置位（与"显式声明不弃用"的意图相反）→ 现改为报错
	wantErrSrc(t, deprecatedFalseSrc, nil, "oapi:deprecated 为单向标记", "只接受空值或 true", "false")
}

func TestDeprecatedAnnotationRejectsGarbage(t *testing.T) {
	wantErrSrc(t, deprecatedGarbageSrc, nil, "oapi:deprecated 为单向标记")
}

// ---- FileStream 端点状态码护栏：流成功码由适配器固定，声明非 200 会静默丢弃 ----

const streamStatusBadSrc = `package app

import (
	"context"

	"github.com/EdSan845D/oapi-hinge/hinge"
)

// oapi:prefix /f
type FileEp struct{}

// oapi:route GET /x
// oapi:status 201
func (ep FileEp) X(ctx context.Context, _ any) (*hinge.FileStream, error) { return nil, nil }
`

const streamStatusWrappedBadSrc = `package app

import (
	"context"

	"github.com/EdSan845D/oapi-hinge/hinge"
)

// oapi:prefix /f
type FileEp struct{}

// oapi:route GET /x
// oapi:status 206
func (ep FileEp) X(ctx context.Context, _ any) (hinge.Response[*hinge.FileStream], error) {
	return hinge.Response[*hinge.FileStream]{}, nil
}
`

const streamStatusOkSrc = `package app

import (
	"context"

	"github.com/EdSan845D/oapi-hinge/hinge"
)

// oapi:prefix /f
type FileEp struct{}

// oapi:route GET /a
func (ep FileEp) A(ctx context.Context, _ any) (*hinge.FileStream, error) { return nil, nil }

// oapi:route GET /b
// oapi:status 200
func (ep FileEp) B(ctx context.Context, _ any) (*hinge.FileStream, error) { return nil, nil }
`

func TestStreamStatusGuardRejectsNon200(t *testing.T) {
	wantErrSrc(t, streamStatusBadSrc, nil, "不适用于 FileStream 响应")
}

func TestStreamStatusGuardRejectsWrappedNon200(t *testing.T) {
	wantErrSrc(t, streamStatusWrappedBadSrc, nil, "不适用于 FileStream 响应")
}

func TestStreamStatusGuardAllowsDefaultAnd200(t *testing.T) {
	eps, err := buildIR([]*Package{parseTestPkg(t, "app", streamStatusOkSrc)}, nil)
	if err != nil {
		t.Fatalf("buildIR: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("eps = %d, want 2", len(eps))
	}
	// 未声明 → 0（运行期视同 200）；显式 200 → 放行
	for _, ep := range eps {
		if ep.Status != 0 && ep.Status != 200 {
			t.Fatalf("%s Status = %d，应放行", ep.Handler, ep.Status)
		}
	}
}

// 护栏置于 applyEntryPointConfig 之后，故 FuncDecls.DefaultStatusCode 覆写同样受检
// （该来源不是注解，生成期只在此处可见）。
func TestStreamStatusGuardRejectsConfigOverride(t *testing.T) {
	cfg := []EntryPointConfig{{
		Name: "FileEp",
		FuncDecls: map[FuncId]RouteMeta{
			"app.FileEp.A": {DefaultStatusCode: 201},
		},
	}}
	wantErrSrc(t, streamStatusOkSrc, cfg, "不适用于 FileStream 响应")
}
