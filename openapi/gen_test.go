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

// ============ path 参数类型取自 Q + 401 只随鉴权中间件名声明 ============

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
	// ③ 公开接口不再硬编码 401（401 只随鉴权中间件名按需声明）
	if strings.Contains(s, "401") {
		t.Fatalf("global 401 should be gone:\n%s", s)
	}
}

// ============ Middleware 名命中 securitySchemes → security + 401 ============

func TestGenerateAuthAndExtensions(t *testing.T) {
	eps := []hinge.Endpoint{
		{
			Owner: "t", Handler: "Admin",
			Method: "GET", Path: "/doc/admin", Summary: "受保护接口",
			Middleware: []string{"BearerAuth"},
			RType:      hinge.Type[map[string]string](),
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
	// 未注册同名 scheme 的中间件名不再推导 security
	if strings.Contains(s, "x-rate-limit") {
		t.Fatalf("x-rate-limit should be gone:\n%s", s)
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

// ---- 中间件文档钩子：MWRefs 全限定引用配对（RegisterMiddlewareDoc）----

// 钩子配对用的样例中间件（反射名 = 本包全限定名，与 MWRefs 条目同构）
func demoSessionMW() {}

func TestGenerateMiddlewareDocHook(t *testing.T) {
	const demoRef = "github.com/EdSan845D/oapi-hinge/openapi.demoSessionMW"
	eps := []hinge.Endpoint{
		{
			Owner: "t", Handler: "Del",
			Method: "DELETE", Path: "/doc/users/{id}", Summary: "删除",
			RType:  hinge.Type[map[string]string](),
			MWRefs: []string{demoRef},
		},
		{
			Owner: "t", Handler: "Get",
			Method: "GET", Path: "/doc/users/{id}", Summary: "详情（无该中间件）",
			RType: hinge.Type[map[string]string](),
		},
	}
	RegisterMiddlewareDoc(demoSessionMW, func(op *openapi3.Operation) {
		op.Responses.Set("403", &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription("Forbidden：缺少 X-SessionId 请求头")})
	})
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, eps); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "403") || !strings.Contains(s, "X-SessionId") {
		t.Fatalf("middleware doc hook not applied:\n%s", s)
	}
	// 钩子仅作用于引用了该中间件的端点：按 operation 分块校验
	for _, block := range strings.Split(s, "operationId:") {
		if strings.Contains(block, "t_Del") && !strings.Contains(block, "403") {
			t.Fatalf("403 missing on hooked operation:\n%s", block)
		}
		if strings.Contains(block, "t_Get") && strings.Contains(block, "403") {
			t.Fatalf("403 should not leak to unhooked operation:\n%s", block)
		}
	}
}

// 未消费钩子警告：注册了但没有任何端点 MWRefs 引用 → buildDoc warnings（P0-2）
func TestUnmatchedMiddlewareHookWarning(t *testing.T) {
	RegisterMiddlewareDoc(demoUnmatchedMW, func(op *openapi3.Operation) {})
	// demoSessionMW 已在 TestGenerateMiddlewareDocHook 注册并被消费；
	// demoUnmatchedMW 从未被任何端点 MWRefs 引用，断言警告出现
	_, warnings, err := buildDoc([]hinge.Endpoint{
		{Owner: "t", Handler: "Plain", Method: "GET", Path: "/doc/plain", Summary: "无中间件", RType: hinge.Type[map[string]string]()},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "demoUnmatchedMW") {
		t.Fatalf("unmatched hook warning missing:\n%s", joined)
	}
}

func demoUnmatchedMW() {}
func TestRegisterMiddlewareDocPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	RegisterMiddlewareDoc(demoSessionMW, func(op *openapi3.Operation) {}) // 重复注册
}
