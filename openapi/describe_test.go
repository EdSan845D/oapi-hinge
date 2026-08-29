//go:build openapi

package openapi

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"

	"github.com/getkin/kin-openapi/openapi3"

	testdataa "github.com/EdSan845D/oapi-hinge/openapi/testdata/a"
	datab "github.com/EdSan845D/oapi-hinge/openapi/testdata/b"
)

// ============ DescribeRoute：错误响应 / 响应头 / OperationID / Hide / 未匹配警告 ============

func descEchoHandler(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	return map[string]string{}, nil
}

func TestDescribeRouteErrorsAndHeaders(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	DescribeRoute(descEchoHandler, RouteDoc{
		Errors: []ErrorDecl{
			{Status: http.StatusNotFound, Description: "用户不存在"},
		},
		ResponseHeaders: []HeaderDecl{
			{Name: "X-RateLimit-Remaining", Description: "剩余配额"},
		},
	})

	groups := []*contract.Group{{
		Prefix: "/d",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "演示", Handler: descEchoHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)

	// ① 错误响应声明：404 + 描述 + 推导 code 默认值（跟随状态码）
	if !strings.Contains(s, `"404"`) || !strings.Contains(s, "用户不存在") {
		t.Fatalf("error declaration missing:\n%s", s)
	}
	if !strings.Contains(s, "default: 404") {
		t.Fatalf("derived code default missing:\n%s", s)
	}
	// ② 响应头声明
	if !strings.Contains(s, "X-RateLimit-Remaining") || !strings.Contains(s, "剩余配额") {
		t.Fatalf("response header declaration missing:\n%s", s)
	}
}

func TestDescribeRouteOperationID(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	DescribeRoute(descEchoHandler, RouteDoc{OperationID: "echoOp"})

	groups := []*contract.Group{{
		Prefix: "/d",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "演示", Handler: descEchoHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)
	if !strings.Contains(s, "operationId: echoOp") {
		t.Fatalf("operationId override missing:\n%s", s)
	}
}

func TestDescribeRouteHide(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	DescribeRoute(descEchoHandler, RouteDoc{Hide: true})

	groups := []*contract.Group{{
		Prefix: "/d",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "内部接口", Handler: descEchoHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)
	if strings.Contains(s, "/d/x") || strings.Contains(s, "内部接口") {
		t.Fatalf("hidden route leaked into doc:\n%s", s)
	}
}

func TestDescribeRouteUnmatchedWarning(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	// 注册一个不存在于路由树的 handler → Generate 结束时警告
	DescribeRoute(docHeaderHandler, RouteDoc{})

	groups := []*contract.Group{{
		Prefix: "/d",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "演示", Handler: descEchoHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	warns, err := generate(out, groups, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "docHeaderHandler") && strings.Contains(w, "未匹配到任何路由") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unmatched registration warning missing: %v", warns)
	}
}

func TestDescribeRouteDuplicatePanics(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate DescribeRoute")
		}
	}()
	DescribeRoute(descEchoHandler, RouteDoc{})
	DescribeRoute(descEchoHandler, RouteDoc{})
}

func TestDescribeRouteInvalidStatusPanics(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on invalid ErrorDecl.Status")
		}
	}()
	DescribeRoute(descEchoHandler, RouteDoc{Errors: []ErrorDecl{{Status: 0}}})
}

// ============ OptionWithEnvelope：壳实例推导（文档与运行时同构） ============

type testEnvelope struct{}

type testEnvelopeBody struct {
	OK   bool `json:"ok"`
	Item any  `json:"item"`
}

func (testEnvelope) Success(status int, data any) any {
	return testEnvelopeBody{OK: true, Item: data}
}

func (testEnvelope) Failure(status, code int, msg string) any {
	return testEnvelopeBody{OK: false, Item: msg}
}

func TestOptionWithEnvelopeDerivation(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	groups := []*contract.Group{{
		Prefix: "/e",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "壳推导", Handler: descEchoHandler,
			}),
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/nf", Summary: "错误推导", Handler: descEchoNotFoundHandler,
			}),
		},
	}}
	DescribeRoute(descEchoNotFoundHandler, RouteDoc{
		Errors: []ErrorDecl{{Status: http.StatusNotFound, Description: "没有"}},
	})

	out := t.TempDir() + "/spec.yaml"
	err := Generate(out, groups, OptionWithEnvelope(testEnvelope{}))
	if err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)

	// 成功响应体 = 自定义壳推导（不再有默认壳的 code/msg）
	if !strings.Contains(s, "ok:") || !strings.Contains(s, "item:") {
		t.Fatalf("envelope derivation missing:\n%s", s)
	}
	if strings.Contains(s, "msg:") {
		t.Fatalf("default envelope leaked:\n%s", s)
	}
	// 失败响应体 = 同一壳推导
	if !strings.Contains(s, `"404"`) {
		t.Fatalf("declared error missing:\n%s", s)
	}
}

func descEchoNotFoundHandler(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	return nil, contract.NotFound("没有")
}

// ============ 两阶段命名：跨包同名组件 / operationID ============

func TestSchemaNameCollision(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	groups := []*contract.Group{{
		Prefix: "/n",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, testdataa.User]{
				Method: "GET", Path: "/a", Summary: "A 用户", Handler: testdataa.Health,
			}),
			contract.New(contract.RouteMeta[contract.NoReq, any, datab.User]{
				Method: "GET", Path: "/b", Summary: "B 用户", Handler: datab.Health,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	warns, err := generate(out, groups, false)
	if err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)

	// 组件名升级为 末段包名_裸名，且引用与组件一一对应
	if !strings.Contains(s, "a_User") || !strings.Contains(s, "b_User") {
		t.Fatalf("upgraded component names missing:\n%s", s)
	}
	if !strings.Contains(s, "#/components/schemas/a_User") ||
		!strings.Contains(s, "#/components/schemas/b_User") {
		t.Fatalf("upgraded refs missing:\n%s", s)
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "schema name collision") {
			found = true
		}
	}
	if !found {
		t.Fatalf("collision warning missing: %v", warns)
	}
}

func TestOperationIDCollisionWarning(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	DescribeRoute(testdataa.Health, RouteDoc{OperationID: "aHealth"})

	groups := []*contract.Group{{
		Prefix: "/n",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, testdataa.User]{
				Method: "GET", Path: "/a", Summary: "A", Handler: testdataa.Health,
			}),
			contract.New(contract.RouteMeta[contract.NoReq, any, datab.User]{
				Method: "GET", Path: "/b", Summary: "B", Handler: datab.Health,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	warns, err := generate(out, groups, false)
	if err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)

	// a.Health 已注册 custom ID；b.Health 裸名 Health 不再与之冲突
	if !strings.Contains(s, "operationId: aHealth") || !strings.Contains(s, "operationId: Health") {
		t.Fatalf("operationIds wrong:\n%s", s)
	}
	for _, w := range warns {
		if strings.Contains(w, "duplicate operationID") {
			t.Fatalf("unexpected duplicate warning after registration: %v", warns)
		}
	}
}

// ============ ManualPath / Deprecated / ParamBinderSchema / 严格模式 ============

func TestManualPath(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	item := &openapi3.PathItem{}
	op := openapi3.NewOperation()
	op.Summary = "legacy ping"
	op.Responses = openapi3.NewResponses()
	item.SetOperation(http.MethodGet, op)
	RegisterManualPath("/legacy/ping", item)

	groups := []*contract.Group{{
		Prefix: "/m",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "模板路由", Handler: descEchoHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)
	if !strings.Contains(s, "/legacy/ping") || !strings.Contains(s, "legacy ping") {
		t.Fatalf("manual path missing:\n%s", s)
	}
}

func TestManualPathConflictPanics(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	item := &openapi3.PathItem{}
	op := openapi3.NewOperation()
	op.Responses = openapi3.NewResponses()
	item.SetOperation(http.MethodGet, op)
	RegisterManualPath("/m/x", item) // 与模板路由 GET /m/x 冲突

	groups := []*contract.Group{{
		Prefix: "/m",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "模板路由", Handler: descEchoHandler,
			}),
		},
	}}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on manual path conflict")
		}
	}()
	_ = Generate(t.TempDir()+"/spec.yaml", groups)
}

func TestDeprecatedFlag(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	groups := []*contract.Group{{
		Prefix: "/dep",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/old", Summary: "旧接口", Deprecated: true,
				Handler: descEchoHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readSpec(t, out), "deprecated: true") {
		t.Fatal("deprecated flag missing")
	}
}

func TestParamBinderSchema(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	type binderIDs []int64
	contract.RegisterParamBinder(func(src []string) (binderIDs, error) { return nil, nil })

	arr := openapi3.NewArraySchema()
	arr.Items = &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()}
	RegisterParamBinderSchema[binderIDs](arr)

	type listReq struct {
		IDs binderIDs `query:"ids"`
	}
	listHandler := func(ctx context.Context, req listReq, _ any) (map[string]string, error) {
		return map[string]string{}, nil
	}

	groups := []*contract.Group{{
		Prefix: "/pb",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[listReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "绑定器", Handler: listHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)
	if !strings.Contains(s, "type: array") {
		t.Fatalf("binder doc schema missing:\n%s", s)
	}
}

func TestGenerateStrictFailsOnWarnings(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	groups := []*contract.Group{{
		Prefix: "/s",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/x", Handler: descEchoHandler, // 缺 Summary → 警告
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if _, err := generate(out, groups, true); err == nil {
		t.Fatal("strict mode should fail on warnings")
	}
}

func readSpec(t *testing.T, out string) string {
	t.Helper()
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// ============ 中间件钩子回归：钩子内写 Responses 不得 panic（Responses 先于钩子初始化） ============

func TestMiddlewareHookWritesResponses(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	fakeAuth := func(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
		return map[string]string{}, nil
	}
	RegisterMiddlewareDoc(fakeAuth, func(op *openapi3.Operation) {
		op.Security = &openapi3.SecurityRequirements{{"BearerAuth": {}}}
		op.Responses.Set("401", &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription("Unauthorized")})
	})

	groups := []*contract.Group{{
		Prefix:      "/hk",
		Middlewares: []any{fakeAuth},
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "受保护", Handler: descEchoHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)
	if !strings.Contains(s, `"401"`) || !strings.Contains(s, "Unauthorized") {
		t.Fatalf("hook-written 401 response missing:\n%s", s)
	}
}

// ============ 注释即文档：源码注释解析 + 自定义解析器 ============

func TestSourceComments(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	groups := []*contract.Group{{
		Prefix: "/c",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, testdataa.User]{
				Method: "GET", Path: "/a", Handler: testdataa.Health,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups, OptionWithSourceComments()); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)

	// ① handler 注释首行 → Summary，其余 → Description
	if !strings.Contains(s, "A 包健康检查") || !strings.Contains(s, "返回 A 包用户信息") {
		t.Fatalf("handler comment fallback missing:\n%s", s)
	}
	// ② 结构体注释 → 组件描述
	if !strings.Contains(s, "A 包用户（注释即文档样例）") {
		t.Fatalf("struct comment description missing:\n%s", s)
	}
	// ③ 字段注释 → description
	if !strings.Contains(s, "用户ID，全局唯一") {
		t.Fatalf("field comment description missing:\n%s", s)
	}
	// ④ description 标签优先于注释
	if !strings.Contains(s, "邮箱(标签优先)") {
		t.Fatalf("tag description missing:\n%s", s)
	}
	if strings.Contains(s, "注释不应出现") {
		t.Fatalf("comment should lose to tag:\n%s", s)
	}
}

func TestCustomCommentParser(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	// 约定：// 描述 | 示例:值
	RegisterCommentParser(func(src string, sch *openapi3.SchemaRef) *openapi3.SchemaRef {
		parts := strings.SplitN(src, "|", 2)
		desc := strings.TrimSpace(parts[0])
		example := ""
		if len(parts) == 2 {
			example = strings.TrimPrefix(strings.TrimSpace(parts[1]), "示例:")
		}
		return DescribeSchema(sch, desc, example)
	})

	groups := []*contract.Group{{
		Prefix: "/cp",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, datab.User]{
				Method: "GET", Path: "/b", Handler: datab.Health,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups, OptionWithSourceComments()); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)
	if !strings.Contains(s, "用户名") || !strings.Contains(s, "example: alice") {
		t.Fatalf("custom parser output missing:\n%s", s)
	}
}

func TestCommentParserWithoutOptionWarns(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	RegisterCommentParser(func(src string, sch *openapi3.SchemaRef) *openapi3.SchemaRef {
		return sch
	})

	groups := []*contract.Group{{
		Prefix: "/w",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "演示", Handler: descEchoHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	warns, err := generate(out, groups, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "未开启 OptionWithSourceComments") {
			found = true
		}
	}
	if !found {
		t.Fatalf("comment-parser warning missing: %v", warns)
	}
}

func TestRegisterCommentParserPanics(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate RegisterCommentParser")
		}
	}()
	RegisterCommentParser(func(src string, sch *openapi3.SchemaRef) *openapi3.SchemaRef { return sch })
	RegisterCommentParser(func(src string, sch *openapi3.SchemaRef) *openapi3.SchemaRef { return sch })
}
