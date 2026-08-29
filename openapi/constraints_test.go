//go:build openapi

package openapi

import (
	"context"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/contract/response"

	"github.com/getkin/kin-openapi/openapi3"

	testdataa "github.com/EdSan845D/oapi-hinge/openapi/testdata/a"
)

// ============ 约束映射：validate/binding 标签 → OpenAPI 约束 ============

type constraintQueryReq struct {
	Status  string   `query:"status" validate:"oneof=active inactive"`
	Page    int      `query:"page" validate:"gte=1,lte=100"`
	Keyword string   `query:"kw" validate:"min=2,max=10"`
	Email   string   `query:"email" validate:"email"`
	TagIDs  []string `query:"tags" validate:"min=1,max=5"`
	StrictN int      `query:"strict" validate:"gt=0,lt=100"`
	BoundN  int      `query:"bound" binding:"gte=1"`
	OptN    *int     `query:"opt"`
}

func constraintQueryHandler(ctx context.Context, req constraintQueryReq, _ any) (map[string]string, error) {
	return map[string]string{}, nil
}

func TestConstraintMappingQuery(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	groups := []*contract.Group{{
		Prefix: "/cq",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[constraintQueryReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "约束", Handler: constraintQueryHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)

	for _, want := range []string{
		"- active", "- inactive", // oneof → enum（字符串原样）
		"minimum: 1", "maximum: 100", // gte/lte → 数字边界
		"minLength: 2", "maxLength: 10", // min/max → 字符串长度
		"format: email",              // email → format
		"minItems: 1", "maxItems: 5", // 切片 → 项数
		"exclusiveMinimum: 0", "exclusiveMaximum: 100", // gt/lt → 严格边界
		"nullable: true", // 指针字段
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("constraint %q missing:\n%s", want, s)
		}
	}
}

type constraintBodyReq struct {
	Age   int     `json:"age" validate:"gte=18,lte=120"`
	Email string  `json:"email" validate:"email" example:"a@b.com"`
	Level string  `json:"level" validate:"oneof=bronze silver" default:"bronze"`
	Nick  *string `json:"nick"`
}

func constraintBodyHandler(ctx context.Context, _ contract.NoReq, req constraintBodyReq) (map[string]string, error) {
	return map[string]string{}, nil
}

func TestConstraintMappingBody(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	groups := []*contract.Group{{
		Prefix: "/cb",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, constraintBodyReq, map[string]string]{
				Method: "POST", Path: "/x", Summary: "约束", Handler: constraintBodyHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)

	for _, want := range []string{
		"minimum: 18", "maximum: 120", // body 数字约束
		"format: email",
		"example: a@b.com",     // example 标签（字符串原样）
		"- bronze", "- silver", // oneof → enum
		"default: bronze", // body 字段 default 标签
		"nullable: true",  // 指针字段
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("constraint %q missing:\n%s", want, s)
		}
	}
}

func TestExampleTagCoercion(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	type req struct {
		N int  `query:"n" example:"5"`
		B bool `query:"b" example:"true"`
	}
	handler := func(ctx context.Context, r req, _ any) (map[string]string, error) {
		return map[string]string{}, nil
	}
	groups := []*contract.Group{{
		Prefix: "/ex",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[req, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "example 转型", Handler: handler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)
	if !strings.Contains(s, "example: 5") || !strings.Contains(s, "example: true") {
		t.Fatalf("example coercion missing:\n%s", s)
	}
}

// ============ 类型级 schema 覆盖（组件替换） ============

type overriddenType struct {
	V string
}

func TestTypeSchemaOverride(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	RegisterTypeSchema[overriddenType](openapi3.NewFloat64Schema().WithFormat("decimal"))

	groups := []*contract.Group{{
		Prefix: "/to",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, overriddenType, map[string]string]{
				Method: "POST", Path: "/x", Summary: "覆盖", Handler: func(ctx context.Context, _ contract.NoReq, req overriddenType) (map[string]string, error) {
					return map[string]string{}, nil
				},
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)

	// 组件替换：$ref 结构不变，组件内容 = 注册 schema（不再有原字段）
	if !strings.Contains(s, "#/components/schemas/overriddenType") {
		t.Fatalf("$ref missing:\n%s", s)
	}
	if !strings.Contains(s, "format: decimal") || !strings.Contains(s, "type: number") {
		t.Fatalf("overridden component content missing:\n%s", s)
	}
	if strings.Contains(s, "V:") {
		t.Fatalf("overridden component leaked original fields:\n%s", s)
	}
}

func TestTypeSchemaFunc(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	RegisterTypeSchemaFunc[overriddenType](func() *openapi3.Schema {
		return openapi3.NewStringSchema().WithPattern("^ok$")
	})

	groups := []*contract.Group{{
		Prefix: "/tf",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, overriddenType, map[string]string]{
				Method: "POST", Path: "/x", Summary: "函数覆盖", Handler: func(ctx context.Context, _ contract.NoReq, req overriddenType) (map[string]string, error) {
					return map[string]string{}, nil
				},
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)
	if !strings.Contains(s, "pattern: ^ok$") {
		t.Fatalf("func override missing:\n%s", s)
	}
}

// ============ 顶层 tags 声明 / Build API ============

func TestDocTags(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	groups := []*contract.Group{{
		Prefix:      "/tg",
		Description: "用户相关接口",
		Tags:        []string{"用户"},
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
				Method: "GET", Path: "/x", Summary: "演示", Tags: []string{"用户"}, Handler: descEchoHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)
	if !strings.Contains(s, "name: 用户") || !strings.Contains(s, "description: 用户相关接口") {
		t.Fatalf("top-level tags missing:\n%s", s)
	}
}

func TestBuildAPI(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	groups := []*contract.Group{{
		Prefix: "/b",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, testdataa.User]{
				Method: "GET", Path: "/x", Summary: "演示", Handler: testdataa.Health,
			}),
		},
	}}
	doc, err := Build(groups)
	if err != nil {
		t.Fatal(err)
	}
	if doc == nil || doc.Paths == nil {
		t.Fatal("Build returned empty doc")
	}
	if len(doc.Paths.InMatchingOrder()) == 0 {
		t.Fatal("Build doc has no paths")
	}
	if len(doc.Components.Schemas) == 0 {
		t.Fatal("Build doc has no components")
	}
}

// ============ body default 标签全标量类型转型 ============

type defaultTypesReq struct {
	Price   float64 `json:"price" default:"9.9"`
	BigID   int64   `json:"big_id" default:"9007199254740993"`
	Count   uint32  `json:"count" default:"7"`
	Enabled bool    `json:"enabled" default:"true"`
}

func defaultTypesHandler(ctx context.Context, _ contract.NoReq, req defaultTypesReq) (map[string]string, error) {
	return map[string]string{}, nil
}

func TestBodyDefaultTypeCoercion(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	groups := []*contract.Group{{
		Prefix: "/dt",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, defaultTypesReq, map[string]string]{
				Method: "POST", Path: "/x", Summary: "default 转型", Handler: defaultTypesHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)
	for _, want := range []string{
		"default: 9.9",              // 浮点转型（不再保留字符串）
		"default: 9007199254740993", // int64 大数（Atoi 会溢出回退字符串）
		"default: 7",                // uint
		"default: true",             // bool
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("default %q missing:\n%s", want, s)
		}
	}
}

// ============ 泛型组件名合法字符回归：Paged[User] 不再产生非法 / ============

func TestGenericComponentNameCharset(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	pagedHandler := func(ctx context.Context, _ contract.NoReq, _ any) (response.Paged[testdataa.User], error) {
		return response.Paged[testdataa.User]{Items: []testdataa.User{{ID: "a1", Name: "alice"}}, Total: 1}, nil
	}

	groups := []*contract.Group{{
		Prefix: "/pg",
		Routes: []contract.Route{
			contract.New(contract.RouteMeta[contract.NoReq, any, response.Paged[testdataa.User]]{
				Method: "GET", Path: "/x", Summary: "泛型分页", Handler: pagedHandler,
			}),
		},
	}}
	out := t.TempDir() + "/spec.yaml"
	warns, err := generate(out, groups, false)
	if err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)
	// 泛型参数取末段限定名：Paged[...a.User] -> Paged_a_User（合法字符集，无点号）
	if !strings.Contains(s, "Paged_a_User") {
		t.Fatalf("generic component name missing:\n%s", s)
	}
	for _, w := range warns {
		if strings.Contains(w, "spec validation failed") {
			t.Fatalf("generated spec invalid:\n%s", s)
		}
	}
}
