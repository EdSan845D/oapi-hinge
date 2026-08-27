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
