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
)

// 逃生舱文档生成验证：header 标签进文档、Response[R] 解包取 Data schema
// anyBodyHandler 通用无参处理器（B=any 场景复用）
func anyBodyHandler(ctx context.Context, _ contract.NoReq, _ any) (map[string]string, error) {
	return map[string]string{}, nil
}

type docHeaderReq struct {
	Lang string `header:"Accept-Language" description:"语言"`
}

func docHeaderHandler(ctx context.Context, req docHeaderReq, _ any) (map[string]string, error) {
	return map[string]string{}, nil
}

type docUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func docCreatedHandler(ctx context.Context, _ contract.NoReq, _ any) (contract.Response[docUser], error) {
	return contract.Response[docUser]{Status: http.StatusCreated, Data: docUser{}}, nil
}

func TestGenerateEscapeHatches(t *testing.T) {
	groups := []*contract.Group{
		{
			Prefix: "/doc",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[docHeaderReq, any, map[string]string]{
					Method:  "GET",
					Path:    "/h",
					Summary: "header",
					Handler: docHeaderHandler,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, any, contract.Response[docUser]]{
					Method:  "POST",
					Path:    "/c",
					Summary: "created",
					Handler: docCreatedHandler,
				}),
			},
		},
	}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
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

// ============ 升级能力验证：DefaultStatusCode 文档联动 + EnvelopeSchema 可替换 ============

func TestGenerateDefaultStatusCode(t *testing.T) {
	groups := []*contract.Group{
		{
			Prefix: "/doc",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "POST", Path: "/create", Summary: "创建",
					DefaultStatusCode: http.StatusCreated,
					Handler:           anyBodyHandler,
				}),
			},
		},
	}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	// 成功响应码使用路由级 DefaultStatusCode 而非硬编码 200
	if !strings.Contains(s, `"201":`) {
		t.Fatalf("default status code not reflected in doc:\n%s", s)
	}
}

func TestGenerateCustomEnvelopeSchema(t *testing.T) {
	groups := []*contract.Group{
		{
			Prefix: "/doc",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "GET", Path: "/h", Summary: "header",
					Handler: anyBodyHandler,
				}),
			},
		},
	}
	out := t.TempDir() + "/spec.yaml"
	err := Generate(out, groups, OptionWithEnvelopeSchema(func(data *openapi3.SchemaRef) *openapi3.SchemaRef {
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

// ============ path 参数类型取自 Q + 401 不再全局硬编码 ============

type docPathReq struct {
	ID  int    `path:"id" description:"用户ID"`
	Sub string `path:"sub"`
}

func docPathHandler(ctx context.Context, req docPathReq, _ any) (map[string]string, error) {
	return map[string]string{}, nil
}

func TestGeneratePathParamsFromQueryStruct(t *testing.T) {
	groups := []*contract.Group{
		{
			Prefix: "/doc",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[docPathReq, any, map[string]string]{
					Method: "GET", Path: "/users/{id}/{sub}", Summary: "路径参数",
					Handler: docPathHandler,
				}),
				contract.New(contract.RouteMeta[contract.NoReq, any, map[string]string]{
					Method: "GET", Path: "/health", Summary: "公开接口",
					Handler: anyBodyHandler,
				}),
			},
		},
	}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
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
	// ③ 公开接口不再硬编码 401（鉴权 401 由中间件文档钩子按需声明）
	if strings.Contains(s, "401") {
		t.Fatalf("global 401 should be gone:\n%s", s)
	}
}

// ---- ParamBinder 文档联动：注册绑定器的类型，参数 schema 标注为 string ----

type docIDs []string

func init() {
	contract.RegisterParamBinder(func(src []string) (docIDs, error) {
		return docIDs(strings.Split(src[0], ",")), nil
	})
}

type docBinderReq struct {
	Tags docIDs `query:"tags" description:"标签，逗号分隔"`
}

func docBinderHandler(ctx context.Context, req docBinderReq, _ any) (map[string]string, error) {
	return map[string]string{}, nil
}

func TestGenerateParamBinder(t *testing.T) {
	groups := []*contract.Group{
		{
			Prefix: "/doc",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[docBinderReq, any, map[string]string]{
					Method: "GET", Path: "/tags", Summary: "binder",
					Handler: docBinderHandler,
				}),
			},
		},
	}
	out := t.TempDir() + "/spec.yaml"
	if err := Generate(out, groups); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	// binder 类型参数：schema 为 string（而非 docIDs 的 array 形态）
	if !strings.Contains(s, "type: string") {
		t.Fatalf("binder param schema not string:\n%s", s)
	}
	if strings.Contains(s, "type: array") {
		t.Fatalf("binder param leaked array schema:\n%s", s)
	}
}
