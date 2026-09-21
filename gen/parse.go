package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// 包级扫描与 AST 收集：Enterpoint 结构体、方法、oapi:* 注解、
// 结构体字段（供绑定器发射）与类型别名（NoReq = any 识别）。

// structInfo 结构体定义、所在源文件与声明注释（struct 级注解来源）。
type structInfo struct {
	st   *ast.StructType
	file *File
	doc  *ast.CommentGroup
}

// File 单个源文件的解析上下文。
type File struct {
	Filename string
	PkgName  string
	byAlias  map[string]string // 显式别名 -> import path（_/. 不登记）
	byBase   map[string]string // 包基名 -> import path
	structs  map[string]*structInfo
	decls    []ast.Decl // 顶层声明（方法→文件反查用）
}

// importPathOf 把源文件内的包限定符解析为 import path。
func (f *File) importPathOf(alias string) (string, bool) {
	if p, ok := f.byAlias[alias]; ok {
		return p, true
	}
	if p, ok := f.byBase[alias]; ok {
		return p, true
	}
	return "", false
}

// Package 一个被扫描的目录（Enterpoint 所在包）。
type Package struct {
	Dir        string // 相对模块根（POSIX 斜杠）
	ImportPath string // 模块路径 + "/" + Dir
	Name       string // package 名
	Files      []*File
	aliases    map[string]string // 类型别名 name -> "any"（仅登记 any 形态）
	methods    map[string][]*ast.FuncDecl
}

// structOf 按名字查包内结构体（跨文件聚合）。
func (p *Package) structOf(name string) (*structInfo, bool) {
	for _, f := range p.Files {
		if si, ok := f.structs[name]; ok {
			return si, true
		}
	}
	return nil, false
}

// structDoc 结构体声明注释（跨文件取首个非空）。
func (p *Package) structDoc(name string) *ast.CommentGroup {
	for _, f := range p.Files {
		if si, ok := f.structs[name]; ok && si.doc != nil {
			return si.doc
		}
	}
	return nil
}

// methodsOf 接收者类型名的全部方法（值/指针接收者合并）。
func (p *Package) methodsOf(name string) []*ast.FuncDecl {
	return p.methods[name]
}

// annotations 解析注释组中的 oapi:* 注解与其余文档行。
func annotations(doc *ast.CommentGroup) ([][2]string, []string) {
	var kv [][2]string
	var docLines []string
	if doc == nil {
		return kv, docLines
	}
	for _, c := range doc.List {
		text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
		if strings.HasPrefix(text, "oapi:") {
			rest := strings.TrimPrefix(text, "oapi:")
			key, value := rest, ""
			if i := strings.IndexAny(rest, " \t"); i >= 0 {
				key = rest[:i]
				value = strings.TrimSpace(rest[i+1:])
			}
			kv = append(kv, [2]string{key, value})
			continue
		}
		if text != "" {
			docLines = append(docLines, text)
		}
	}
	return kv, docLines
}

// collectDirs 解析全部扫描目录为 Package 列表（跳过 _test.go）。
func collectDirs(fset *token.FileSet, moduleRoot, modulePath string, dirs []string) ([]*Package, error) {
	var pkgs []*Package
	for _, dir := range dirs {
		abs := filepath.Join(moduleRoot, filepath.FromSlash(dir))
		pkgsInDir, err := parser.ParseDir(fset, abs, func(fi fs.FileInfo) bool {
			name := fi.Name()
			return !fi.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
		}, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("解析目录 %s: %w", dir, err)
		}
		for pkgName, pkg := range pkgsInDir {
			p := &Package{
				Dir:        filepath.ToSlash(filepath.Clean(dir)),
				ImportPath: path.Join(modulePath, filepath.ToSlash(filepath.Clean(dir))),
				Name:       pkgName,
				aliases:    map[string]string{},
				methods:    map[string][]*ast.FuncDecl{},
			}
			names := make([]string, 0, len(pkg.Files))
			for name := range pkg.Files {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				f := pkg.Files[name]
				pf := collectFile(fset, name, f)
				p.Files = append(p.Files, pf)
				p.aggregate(f)
			}
			pkgs = append(pkgs, p)
		}
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Dir < pkgs[j].Dir })
	return pkgs, nil
}

// aggregate 把文件内的类型别名与方法聚合到包级。
func (p *Package) aggregate(f *ast.File) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || !ts.Assign.IsValid() {
				continue
			}
			switch t := ts.Type.(type) {
			case *ast.Ident:
				if t.Name == "any" {
					p.aliases[ts.Name.Name] = "any"
				}
			case *ast.InterfaceType:
				if len(t.Methods.List) == 0 {
					p.aliases[ts.Name.Name] = "any"
				}
			}
		}
		continue
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fd.Recv == nil || len(fd.Recv.List) == 0 {
			rootName := PKGFlag + p.Name
			p.methods[rootName] = append(p.methods[rootName], fd)
			continue
		}
		recvName := recvTypeName(fd.Recv.List[0].Type)
		if recvName == "" {
			continue
		}
		p.methods[recvName] = append(p.methods[recvName], fd)
	}
}

// collectFile 建立单文件的 import 索引与结构体索引。
func collectFile(fset *token.FileSet, filename string, f *ast.File) *File {
	pf := &File{
		Filename: filename,
		PkgName:  f.Name.Name,
		byAlias:  map[string]string{},
		byBase:   map[string]string{},
		structs:  map[string]*structInfo{},
		decls:    f.Decls,
	}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if imp.Name != nil {
			alias := imp.Name.Name
			if alias == "_" || alias == "." {
				continue
			}
			pf.byAlias[alias] = p
			continue
		}
		pf.byBase[path.Base(p)] = p
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if st, ok := ts.Type.(*ast.StructType); ok {
				doc := ts.Doc
				if doc == nil {
					doc = gd.Doc
				}
				pf.structs[ts.Name.Name] = &structInfo{st: st, file: pf, doc: doc}
			}
		}
	}
	_ = fset
	return pf
}

// recvTypeName 提取接收者类型名（值/指针接收者）。
func recvTypeName(x ast.Expr) string {
	switch t := x.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}
