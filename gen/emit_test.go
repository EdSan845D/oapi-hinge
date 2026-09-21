package gen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// minimalEndpoint 最小端点 IR：无绑定器、无中间件的 GET 端点。
func minimalEndpoint() *EndpointIR {
	return &EndpointIR{
		Owner:    "T",
		Handler:  "Ping",
		Method:   "GET",
		FullPath: "/ping",
		Pkg:      &Package{ImportPath: "example.com/app", Name: "app"},
		TwoArg:   true,
		RExpr:    ast.NewIdent("string"),
	}
}

func testCfg() Config {
	return Config{Module: "example.com/app", Out: "apigen", Pkg: "apigen", Scan: []string{"./app"}}
}

// TestEmitRegisterSpecCallForm 回归：Spec 调用括号由发射器统一追加，
// 内置模板使用裸 {{.Spec}} —— 产物必须是 SpecTPing() 单次调用（禁止 ()() 双调用）。
func TestEmitRegisterSpecCallForm(t *testing.T) {
	for _, target := range []string{"gin", "echo", "http"} {
		out, err := emitRegister("", testCfg(), []*EndpointIR{minimalEndpoint()}, target)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if got := strings.Count(out, "SpecTPing()"); got != 1 || strings.Contains(out, "()()") {
			t.Fatalf("%s: Spec 调用形态错误（count=%d）:\n%s", target, got, out)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), "register_"+target+"_gen.go", out, 0); err != nil {
			t.Fatalf("%s: 生成产物语法错误: %v", target, err)
		}
	}
}

// TestEmitSpecsLazyFuncs 回归：specs_gen.go 为按需构造函数（导入零分配）。
func TestEmitSpecsLazyFuncs(t *testing.T) {
	specs, err := emitSpecs(testCfg(), []*EndpointIR{minimalEndpoint()})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func SpecTPing() hinge.Endpoint {",
		"SpecTPing(),", // AllSpecs 调用构造
	} {
		if !strings.Contains(specs, want) {
			t.Fatalf("specs 缺少 %q:\n%s", want, specs)
		}
	}
	if strings.Contains(specs, "var SpecTPing") {
		t.Fatalf("specs 不应再发射包级变量:\n%s", specs)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "specs_gen.go", specs, 0); err != nil {
		t.Fatalf("生成产物语法错误: %v", err)
	}
}

// TestEmitDocsLazyFuncs 回归：docs_gen.go 为按需构造函数，Endpoint 字段引用 SpecXxx()。
func TestEmitDocsLazyFuncs(t *testing.T) {
	docs, err := emitDocs(testCfg(), []*EndpointIR{minimalEndpoint()})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func DocSpecTPing() hinge.EndpointDoc {",
		"Endpoint: SpecTPing(),",
	} {
		if !strings.Contains(docs, want) {
			t.Fatalf("docs 缺少 %q:\n%s", want, docs)
		}
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "docs_gen.go", docs, 0); err != nil {
		t.Fatalf("生成产物语法错误: %v", err)
	}
}

// TestBodyKindOf body kind 判定矩阵。
func TestBodyKindOf(t *testing.T) {
	formField := func(name, in string) Field {
		return Field{GoName: name, In: in, Source: name, Class: classScalar, TypeExpr: ast.NewIdent("string"), BaseKind: "string"}
	}
	fileField := func(name string) Field {
		return Field{GoName: name, In: "form", Source: name, Class: classFile, TypeExpr: ast.NewIdent("hinge.FileHeader")}
	}
	cases := []struct {
		name string
		fs   *fieldSet
		want string
	}{
		{"全 form 标签 → form", &fieldSet{Fields: []Field{formField("Pin", "form"), formField("Code", "form")}}, "form"},
		{"空结构体 → json", &fieldSet{}, "json"},
		{"form 与 query 混排 → json", &fieldSet{Fields: []Field{formField("A", "form"), formField("B", "query")}}, "json"},
		{"form 与无标签混排 → json", &fieldSet{Fields: []Field{formField("A", "form"), {GoName: "B", Class: classScalar, TypeExpr: ast.NewIdent("string")}}}, "json"},
		{"含文件字段 → multipart", &fieldSet{Fields: []Field{formField("Name", "form"), fileField("File")}}, "multipart"},
		{"文件字段标签错误也判 multipart（校验由生成期诊断负责）", &fieldSet{Fields: []Field{fileField("File"), formField("F", "query")}}, "multipart"},
	}
	for _, c := range cases {
		if got := bodyKindOf(c.fs); got != c.want {
			t.Errorf("%s: bodyKindOf = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestEmitFormBinder 回归：urlencoded 表单体绑定器从 r.FormValues 取值。
func TestEmitFormBinder(t *testing.T) {
	ep := &EndpointIR{
		Owner: "T", Handler: "ChangePin", Method: "POST", FullPath: "/pin",
		Pkg:     &Package{ImportPath: "example.com/app", Name: "app"},
		HasB:    true, BName: "PinReq", BodyKind: "form",
		BSet: &fieldSet{Fields: []Field{
			{GoName: "PinCode", Access: "v.PinCode", In: "form", Source: "pin_code", Class: classScalar, TypeExpr: ast.NewIdent("string"), BaseKind: "string", Required: true, JSONName: "PinCode"},
			{GoName: "Tags", Access: "v.Tags", In: "form", Source: "tags", Class: classSlice, TypeExpr: ast.NewIdent("string"), BaseKind: "string", JSONName: "Tags"},
		}},
		RExpr: ast.NewIdent("string"),
	}
	out, err := emitBinders(testCfg(), []*EndpointIR{ep})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`r.FormValues("pin_code")`,
		`hinge.Parse[string](vals[0], "pin_code")`,
		`hinge.ParseSlice[string](vals, "tags")`, // string 切片与 query 语义一致：不拆逗号，整段原样解析
		`add("pin_code", "form", "is required")`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("form binder 缺少 %q:\n%s", want, out)
		}
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "binders_gen.go", out, 0); err != nil {
		t.Fatalf("生成产物语法错误: %v", err)
	}
}
