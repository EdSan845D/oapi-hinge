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
