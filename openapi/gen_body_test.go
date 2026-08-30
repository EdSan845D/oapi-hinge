//go:build openapi

package openapi

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"
)

// ============ 新特性测试：RawBody / multipart 的 requestBody 推导 ============

type genUploadReq struct {
	Title string                 `form:"title" binding:"required"`
	Files []*contract.FileHeader `form:"files"`
}

func genRawBodyHandler(ctx context.Context, _ contract.NoReq, b contract.RawBody) (string, error) {
	return string(b), nil
}

func genUploadHandler(ctx context.Context, _ contract.NoReq, b genUploadReq) (map[string]any, error) {
	return nil, nil
}

func TestGenerateBodyKinds(t *testing.T) {
	groups := []*contract.Group{
		{
			Prefix: "/raw",
			Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, contract.RawBody, string]{
				Method: "POST", Path: "", Summary: "原始字节体", Handler: genRawBodyHandler,
			})},
		},
		{
			Prefix: "/up",
			Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, genUploadReq, map[string]any]{
				Method: "POST", Path: "", Summary: "文件上传", Handler: genUploadHandler,
			})},
		},
	}
	out := filepath.Join(t.TempDir(), "spec.yaml")
	if err := Generate(out, groups); err != nil {
		t.Fatalf("generate: %v", err)
	}
	spec := readSpec(t, out)

	// RawBody 路由：octet-stream + binary
	if !strings.Contains(spec, "application/octet-stream") || !strings.Contains(spec, "format: binary") {
		t.Fatalf("raw body spec missing binary format")
	}
	// 上传路由：multipart/form-data，文件字段 binary，title 必填
	if !strings.Contains(spec, "multipart/form-data") {
		t.Fatalf("multipart spec missing")
	}
	for _, want := range []string{"format: binary", "title"} {
		if !strings.Contains(spec, want) {
			t.Fatalf("multipart spec missing %q", want)
		}
	}
	// required 列表存在且包含 title（缩进层级随文档深度变化，只校验成员）
	if !strings.Contains(spec, "required:") || !strings.Contains(spec, "- title") {
		t.Logf("spec excerpt: %s", spec)
		t.Fatalf("multipart required list missing title")
	}
}
