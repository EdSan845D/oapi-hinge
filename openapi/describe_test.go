//go:build openapi

package openapi

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/hinge"

	"github.com/getkin/kin-openapi/openapi3"

	testdataa "github.com/EdSan845D/oapi-hinge/openapi/testdata/a"
	datab "github.com/EdSan845D/oapi-hinge/openapi/testdata/b"
)

// ============ v0.2 端点表范式测试 ============
//
// v0.1 的 DescribeRoute（错误/响应头/OperationID 覆盖/Hide）、ParamBinder
// schema 注册与中间件文档钩子已随路由分组树移除；端点级文档语义由
// Endpoint 字段承载（见 gen.go addOperation）。本文件覆盖保留能力的回归。

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

	eps := []hinge.Endpoint{{
		Owner: "t", Handler: "Echo",
		Method: "GET", Path: "/e/x", Summary: "壳推导",
		RType: hinge.Type[map[string]string](),
	}}
	out := t.TempDir() + "/spec.yaml"
	err := Generate(out, eps, OptionWithEnvelope(testEnvelope{}))
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
}

// ============ 两阶段命名：跨包同名组件 / operationID ============

func TestSchemaNameCollision(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	eps := []hinge.Endpoint{
		{
			Owner: "t", Handler: "A",
			Method: "GET", Path: "/n/a", Summary: "A 用户",
			RType: hinge.Type[testdataa.User](),
		},
		{
			Owner: "t", Handler: "B",
			Method: "GET", Path: "/n/b", Summary: "B 用户",
			RType: hinge.Type[datab.User](),
		},
	}
	out := t.TempDir() + "/spec.yaml"
	warns, err := generate(out, eps, false)
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

	// 同 Owner+Handler 两个端点 → operationID 重复 → 正式轮警告
	eps := []hinge.Endpoint{
		{
			Owner: "n", Handler: "Health",
			Method: "GET", Path: "/n/a", Summary: "A",
			RType: hinge.Type[testdataa.User](),
		},
		{
			Owner: "n", Handler: "Health",
			Method: "GET", Path: "/n/b", Summary: "B",
			RType: hinge.Type[datab.User](),
		},
	}
	out := t.TempDir() + "/spec.yaml"
	warns, err := generate(out, eps, false)
	if err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)
	if !strings.Contains(s, "operationId: n_Health") {
		t.Fatalf("operationId missing:\n%s", s)
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "duplicate operationID") {
			found = true
		}
	}
	if !found {
		t.Fatalf("duplicate operationID warning missing: %v", warns)
	}
}

// ============ ManualPath / Deprecated / 严格模式 ============

func TestManualPath(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	item := &openapi3.PathItem{}
	op := openapi3.NewOperation()
	op.Summary = "legacy ping"
	op.Responses = openapi3.NewResponses()
	item.SetOperation(http.MethodGet, op)
	RegisterManualPath("/legacy/ping", item)

	eps := []hinge.Endpoint{{
		Owner: "t", Handler: "Demo",
		Method: "GET", Path: "/m/x", Summary: "模板路由",
		RType: hinge.Type[map[string]string](),
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, eps); err != nil {
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
	RegisterManualPath("/m/x", item) // 与端点表 GET /m/x 冲突

	eps := []hinge.Endpoint{{
		Owner: "t", Handler: "Demo",
		Method: "GET", Path: "/m/x", Summary: "模板路由",
		RType: hinge.Type[map[string]string](),
	}}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on manual path conflict")
		}
	}()
	_ = Generate(t.TempDir()+"/spec.yaml", eps)
}

func TestDeprecatedFlag(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	eps := []hinge.Endpoint{{
		Owner: "t", Handler: "Old",
		Method: "GET", Path: "/dep/old", Summary: "旧接口",
		Deprecated: true,
		RType:      hinge.Type[map[string]string](),
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, eps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readSpec(t, out), "deprecated: true") {
		t.Fatal("deprecated flag missing")
	}
}

func TestGenerateStrictFailsOnWarnings(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	// 手写端点未填 Summary → 警告 → 严格模式失败
	eps := []hinge.Endpoint{{
		Owner: "t", Handler: "NoSummary",
		Method: "GET", Path: "/s/x",
		RType: hinge.Type[map[string]string](),
	}}
	out := t.TempDir() + "/spec.yaml"
	if _, err := generate(out, eps, true); err == nil {
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

// ============ 注释即文档：源码注释解析 + 自定义解析器 ============

func TestSourceComments(t *testing.T) {
	resetRegistries()
	defer resetRegistries()

	eps := []hinge.Endpoint{{
		Owner: "t", Handler: "A",
		Method: "GET", Path: "/c/a",
		RType: hinge.Type[testdataa.User](),
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, eps, OptionWithSourceComments()); err != nil {
		t.Fatal(err)
	}
	s := readSpec(t, out)

	// ① 结构体注释 → 组件描述
	if !strings.Contains(s, "A 包用户（注释即文档样例）") {
		t.Fatalf("struct comment description missing:\n%s", s)
	}
	// ② 字段注释 → description
	if !strings.Contains(s, "用户ID，全局唯一") {
		t.Fatalf("field comment description missing:\n%s", s)
	}
	// ③ description 标签优先于注释
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

	eps := []hinge.Endpoint{{
		Owner: "t", Handler: "B",
		Method: "GET", Path: "/cp/b",
		RType: hinge.Type[datab.User](),
	}}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, eps, OptionWithSourceComments()); err != nil {
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

	eps := []hinge.Endpoint{{
		Owner: "t", Handler: "Demo",
		Method: "GET", Path: "/w/x", Summary: "演示",
		RType: hinge.Type[map[string]string](),
	}}
	out := t.TempDir() + "/spec.yaml"
	warns, err := generate(out, eps, false)
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
