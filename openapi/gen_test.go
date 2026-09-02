//go:build openapi

package openapi

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/hinge"

	"github.com/getkin/kin-openapi/openapi3"
)

// 定制能力文档生成验证：header 标签进文档、Response[R] 解包取 Data schema。
// v0.2 端点表范式：测试直接构造 []hinge.Endpoint（QType/BType/RType 由 hinge.Type[T]() 填充）。
type docHeaderReq struct {
	Lang string `header:"Accept-Language" description:"语言"`
}

type docUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestGenerateEscapeHatches(t *testing.T) {
	eps := []hinge.Endpoint{
		{
			Owner: "t", Handler: "DocHeader",
			Method: "GET", Path: "/doc/h", Summary: "header",
			QType: hinge.Type[docHeaderReq](), RType: hinge.Type[map[string]string](),
		},
		{
			Owner: "t", Handler: "DocCreated",
			Method: "POST", Path: "/doc/c", Summary: "created",
			RType: hinge.Type[hinge.Response[docUser]](),
		},
	}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, eps); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)

	// ① header 标签 → header 参数
	if !strings.Contains(s, "in: header") || !strings.Contains(s, "Accept-Language") {
		t.Fatalf("header param missing:\n%s", s)
	}

	// ② Response[R] 解包：不得泄漏 Response 组件
	if strings.Contains(s, "Response_") {
		t.Fatalf("Response wrapper leaked into schemas:\n%s", s)
	}

	// ③ data schema 指向 docUser 组件
	if !strings.Contains(s, "docUser:") {
		t.Fatalf("Response.Data schema not resolved:\n%s", s)
	}
}

// ============ 升级能力验证：Status 文档联动 + EnvelopeSchema 可替换 ============

func TestGenerateDefaultStatusCode(t *testing.T) {
	eps := []hinge.Endpoint{
		{
			Owner: "t", Handler: "Create",
			Method: "POST", Path: "/doc/create", Summary: "创建",
			Status: http.StatusCreated,
			RType:  hinge.Type[map[string]string](),
		},
	}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, eps); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	// 成功响应码使用端点级 Status 而非硬编码 200
	if !strings.Contains(s, `"201":`) {
		t.Fatalf("default status code not reflected in doc:\n%s", s)
	}
}

func TestGenerateCustomEnvelopeSchema(t *testing.T) {
	eps := []hinge.Endpoint{
		{
			Owner: "t", Handler: "H",
			Method: "GET", Path: "/doc/h", Summary: "header",
			RType: hinge.Type[map[string]string](),
		},
	}
	out := t.TempDir() + "/spec.yaml"
	err := Generate(out, eps, OptionWithEnvelopeSchema(func(data *openapi3.SchemaRef) *openapi3.SchemaRef {
		obj := openapi3.NewObjectSchema()
		obj.Properties = openapi3.Schemas{"error": data}
		return &openapi3.SchemaRef{Value: obj}
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	// 壳 schema 已替换：{error: data}，不再有 {code, data, msg}
	if strings.Contains(s, "msg:") || strings.Contains(s, "code:") {
		t.Fatalf("default envelope still present:\n%s", s)
	}
	if !strings.Contains(s, "error:") {
		t.Fatalf("custom envelope missing:\n%s", s)
	}
}

// ============ path 参数类型取自 Q + 401 只随 ep.Auth 声明 ============

type docPathReq struct {
	ID  int    `path:"id" description:"用户ID"`
	Sub string `path:"sub"`
}

func TestGeneratePathParamsFromQueryStruct(t *testing.T) {
	eps := []hinge.Endpoint{
		{
			Owner: "t", Handler: "DocPath",
			Method: "GET", Path: "/doc/users/{id}/{sub}", Summary: "路径参数",
			QType: hinge.Type[docPathReq](), RType: hinge.Type[map[string]string](),
		},
		{
			Owner: "t", Handler: "Health",
			Method: "GET", Path: "/doc/health", Summary: "公开接口",
			RType: hinge.Type[map[string]string](),
		},
	}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, eps); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)

	// ① path 参数类型取自 Q 字段（int → integer）
	if !strings.Contains(s, "integer") {
		t.Fatalf("path param type not taken from Q struct:\n%s", s)
	}
	// ② path 参数描述来自 description 标签
	if !strings.Contains(s, "用户ID") {
		t.Fatalf("path param description missing:\n%s", s)
	}
	// ③ 公开接口不再硬编码 401（401 只随 ep.Auth 按需声明）
	if strings.Contains(s, "401") {
		t.Fatalf("global 401 should be gone:\n%s", s)
	}
}

// ============ ep.Auth → security + 401；ep.Limit/Timeout → 扩展字段 ============

func TestGenerateAuthAndExtensions(t *testing.T) {
	eps := []hinge.Endpoint{
		{
			Owner: "t", Handler: "Admin",
			Method: "GET", Path: "/doc/admin", Summary: "受保护接口",
			Auth: "BearerAuth", Limit: "120/min",
			RType: hinge.Type[map[string]string](),
		},
	}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, eps, OptionWithSecurity(openapi3.SecuritySchemes{
		"BearerAuth": &openapi3.SecuritySchemeRef{Value: openapi3.NewSecurityScheme().
			WithType("http").WithScheme("bearer")},
	})); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "BearerAuth") {
		t.Fatalf("security scheme missing:\n%s", s)
	}
	if !strings.Contains(s, `"401":`) {
		t.Fatalf("401 response missing for auth endpoint:\n%s", s)
	}
	if !strings.Contains(s, "x-rate-limit") {
		t.Fatalf("x-rate-limit extension missing:\n%s", s)
	}
}

// ---- 切片 query 参数：array schema（v0.2 移除 ParamBinder 注册表后的形态）----

type docSliceReq struct {
	Tags []string `query:"tags" description:"标签，逗号分隔"`
}

func TestGenerateSliceQueryParam(t *testing.T) {
	eps := []hinge.Endpoint{
		{
			Owner: "t", Handler: "Tags",
			Method: "GET", Path: "/doc/tags", Summary: "tags",
			QType: hinge.Type[docSliceReq](), RType: hinge.Type[map[string]string](),
		},
	}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, eps); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "type: array") {
		t.Fatalf("slice param schema not array:\n%s", s)
	}
}
