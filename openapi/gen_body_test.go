//go:build openapi

package openapi

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/hinge"
)

// ============ RawBody / multipart 的 requestBody 推导（v0.2 端点表构造）============

type genUploadReq struct {
	Title string                `form:"title" binding:"required"`
	Files []*hinge.FileHeader `form:"files"`
}

func TestGenerateBodyKinds(t *testing.T) {
	eps := []hinge.Endpoint{
		{
			Owner: "t", Handler: "Raw",
			Method: "POST", Path: "/raw", Summary: "原始字节体",
			BType: hinge.Type[hinge.RawBody](), RType: hinge.Type[string](),
		},
		{
			Owner: "t", Handler: "Upload",
			Method: "POST", Path: "/up", Summary: "文件上传",
			BType: hinge.Type[genUploadReq](), RType: hinge.Type[map[string]any](),
		},
	}
	out := filepath.Join(t.TempDir(), "spec.yaml")
	if err := Generate(out, eps); err != nil {
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

type genCookieReq struct {
	SID string `cookie:"sid" binding:"required"`
}

func TestCookieParameterDoc(t *testing.T) {
	eps := []hinge.Endpoint{
		{
			Owner: "t", Handler: "Cookie",
			Method: "GET", Path: "/c",
			QType: hinge.Type[genCookieReq](), RType: hinge.Type[map[string]string](),
		},
	}
	out := filepath.Join(t.TempDir(), "spec.yaml")
	if err := Generate(out, eps); err != nil {
		t.Fatalf("generate: %v", err)
	}
	spec := readSpec(t, out)
	if !strings.Contains(spec, "in: cookie") || !strings.Contains(spec, "sid") {
		t.Fatalf("cookie param missing in spec")
	}
}
