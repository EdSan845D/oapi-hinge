package openapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
)

// ============ 注释即文档（源码解析） ============
//
// 仅在 OptionWithSourceComments 开启后生效，且只解析主模块（go.mod 所在目录）下的包：
//   - 字段上方注释 → query/header/path 参数与 body 字段的 description
//   - 结构体上方注释 → components 组件 description
//   - handler 函数上方注释 → operation Summary/Description 兜底
//
// 解析结果按包目录缓存；构建期懒加载。注释只存在于源码，release 二进制零参与。
// 优先级：description 标签 > 注释解析结果（内置默认解析器遵守；自定义解析器自决）。

// CommentParser 字段/类型注释解析器。
// src：注释原文（多行保留换行）；
// sch：当前 schema 引用——内联 schema 时 Value 非 nil 可直接改；
// 字段类型是命名结构体时为 $ref（Value 为 nil），可用 DescribeSchema 包装。
// 返回最终生效的引用。
type CommentParser func(src string, sch *openapi3.SchemaRef) *openapi3.SchemaRef

var (
	// sourceComments 由 OptionWithSourceComments 开启（generate 每轮重置后由 opts 重设）
	sourceComments bool
	// commentParser 自定义注释解析器；nil = 内置默认解析
	commentParser CommentParser

	astMu       sync.Mutex
	modulePath  string
	moduleRoot  string
	moduleFound bool
	pkgCache    = map[string]*pkgDocs{} // 包目录 -> 解析结果（含失败空结果）
	fileCache   = map[string]*pkgDocs{} // 单文件 -> 解析结果（handler 注释用）
)

// typeDoc 单个结构体的源码注释文档。
type typeDoc struct {
	description string            // 结构体上方注释
	fields      map[string]string // Go 字段名 -> 注释
}

// pkgDocs 单个包目录的解析结果。
type pkgDocs struct {
	typeDocs map[string]*typeDoc // TypeSpec 名（源声明名）-> 文档
	funcDocs map[string]string   // 函数名 -> 注释
}

// findModule 从 CWD 向上查找 go.mod，返回主模块路径与所在目录（结果缓存）。
func findModule() (string, string) {
	astMu.Lock()
	defer astMu.Unlock()
	if moduleFound {
		return modulePath, moduleRoot
	}
	moduleFound = true
	dir, err := os.Getwd()
	if err != nil {
		return "", ""
	}
	for {
		if data, rerr := os.ReadFile(filepath.Join(dir, "go.mod")); rerr == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					modulePath = strings.TrimSpace(strings.TrimPrefix(line, "module"))
					moduleRoot = dir
					break
				}
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return modulePath, moduleRoot
}

// typeDocsFor 返回类型的源码注释文档；未开启/非主模块/无法解析返回 nil。
func typeDocsFor(t reflect.Type) *typeDoc {
	if !sourceComments || t == nil || t.Name() == "" || t.PkgPath() == "" {
		return nil
	}
	name := t.Name()
	if i := strings.Index(name, "["); i >= 0 {
		name = name[:i] // 泛型实例取源声明名
	}
	td := pkgDocsFor(pkgDirOf(t.PkgPath())).typeDocs[name]
	return td
}

// handlerCommentOf 取 handler 函数的源码注释（按函数入口定位源文件，主模块外照常解析不到）。
func handlerCommentOf(handlerFn any) string {
	if !sourceComments || handlerFn == nil {
		return ""
	}
	v := reflect.ValueOf(handlerFn)
	fn := runtime.FuncForPC(v.Pointer())
	if fn == nil {
		return ""
	}
	file, _ := fn.FileLine(fn.Entry())
	if file == "" {
		return ""
	}
	// 函数名：剥包路径与方法接收者（如 pkg.(*T).Method -fm → Method）
	name := fn.Name()
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(name, "-fm")
	return parseFileDocs(file).funcDocs[name]
}

// pkgDirOf 包路径 → 主模块内目录；非主模块返回 ""（跳过解析）。
func pkgDirOf(pkgPath string) string {
	mp, root := findModule()
	if mp == "" || root == "" {
		return ""
	}
	if pkgPath == mp {
		return root
	}
	if !strings.HasPrefix(pkgPath, mp+"/") {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pkgPath, mp+"/")))
}

// pkgDocsFor 解析目录下全部 .go 文件的注释（_test.go 除外；解析失败按空处理）。
func pkgDocsFor(dir string) *pkgDocs {
	astMu.Lock()
	defer astMu.Unlock()
	if docs, ok := pkgCache[dir]; ok {
		return docs
	}
	docs := &pkgDocs{typeDocs: map[string]*typeDoc{}, funcDocs: map[string]string{}}
	pkgCache[dir] = docs
	if dir == "" {
		return docs
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return docs
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f := parseGoFile(path)
		if f != nil {
			collectPkgDocs(docs, f)
		}
	}
	return docs
}

// parseFileDocs 解析单个源文件的注释（handler 注释按入口文件精确定位）。
func parseFileDocs(file string) *pkgDocs {
	astMu.Lock()
	defer astMu.Unlock()
	if docs, ok := fileCache[file]; ok {
		return docs
	}
	docs := &pkgDocs{typeDocs: map[string]*typeDoc{}, funcDocs: map[string]string{}}
	fileCache[file] = docs
	if f := parseGoFile(file); f != nil {
		collectPkgDocs(docs, f)
	}
	return docs
}

func parseGoFile(path string) *ast.File {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	return f
}

// collectPkgDocs 从 AST 收集结构体/字段/函数注释。
func collectPkgDocs(docs *pkgDocs, f *ast.File) {
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				td := &typeDoc{fields: map[string]string{}}
				if txt := commentText(d.Doc); txt != "" {
					td.description = txt
				} else if txt := commentText(ts.Doc); txt != "" {
					td.description = txt
				}
				for _, field := range st.Fields.List {
					txt := commentText(field.Doc)
					if txt == "" {
						txt = commentText(field.Comment)
					}
					if txt == "" {
						continue
					}
					for _, n := range field.Names {
						td.fields[n.Name] = txt
					}
				}
				docs.typeDocs[ts.Name.Name] = td
			}
		case *ast.FuncDecl:
			if d.Recv == nil {
				if txt := commentText(d.Doc); txt != "" {
					docs.funcDocs[d.Name.Name] = txt
				}
			}
		}
	}
}

// commentText 提取注释组文本：剥注释符、剔除 //go: 等指令注释，多行保留换行。
func commentText(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	return strings.TrimSpace(cg.Text())
}

// defaultCommentParser 内置解析：注释原文 → description（尊重已有 description，标签优先）。
func defaultCommentParser(src string, sch *openapi3.SchemaRef) *openapi3.SchemaRef {
	if sch.Value != nil {
		if sch.Value.Description == "" {
			sch.Value.Description = src
		}
		return sch
	}
	return DescribeSchema(sch, src, "")
}

// runCommentParser 应用注释解析器（自定义优先，缺省内置）。
func runCommentParser(src string, sch *openapi3.SchemaRef) *openapi3.SchemaRef {
	p := commentParser
	if p == nil {
		p = defaultCommentParser
	}
	return p(src, sch)
}

// applyFieldComment 字段注释 → schema（供 buildStruct / queryParams / path 参数复用）。
func applyFieldComment(sch *openapi3.SchemaRef, t reflect.Type, goField string) *openapi3.SchemaRef {
	td := typeDocsFor(t)
	if td == nil {
		return sch
	}
	src := td.fields[goField]
	if src == "" {
		return sch
	}
	return runCommentParser(src, sch)
}

// DescribeSchema 给 schema 引用附加描述/示例（$ref 用 AllOf 包装）。
// 供自定义 CommentParser 实现；已有描述/示例时不覆盖。
func DescribeSchema(sch *openapi3.SchemaRef, description, example string) *openapi3.SchemaRef {
	if sch == nil {
		return sch
	}
	if sch.Value != nil {
		if description != "" && sch.Value.Description == "" {
			sch.Value.Description = description
		}
		if example != "" && sch.Value.Example == nil {
			sch.Value.Example = example
		}
		return sch
	}
	wrapper := &openapi3.Schema{AllOf: []*openapi3.SchemaRef{sch}}
	if description != "" {
		wrapper.Description = description
	}
	if example != "" {
		wrapper.Example = example
	}
	return &openapi3.SchemaRef{Value: wrapper}
}
