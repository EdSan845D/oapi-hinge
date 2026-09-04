package gen

import (
	"fmt"
	"go/ast"
	"reflect"
	"sort"
	"strings"
)

// 类型表达式渲染器：把源文件中的类型表达式渲染到目标生成文件的上下文，
// 同时收集目标文件所需的 import。table 模式渲染目标为业务包内文件
// （本地类型不加限定、本包 selector 不 import）。

type importSet struct {
	byPath map[string]string // path -> alias（"" = 默认包名）
	order  []string
}

func newImportSet() *importSet {
	return &importSet{byPath: map[string]string{}}
}

func (is *importSet) add(p, alias string) {
	if p == "" {
		return
	}
	if prev, ok := is.byPath[p]; ok {
		if alias != "" && prev == "" {
			is.byPath[p] = alias
		}
		return
	}
	is.byPath[p] = alias
	is.order = append(is.order, p)
}

// block 渲染 import 块（按路径排序；无 import 返回空串）。
func (is *importSet) block() string {
	if len(is.order) == 0 {
		return ""
	}
	paths := append([]string{}, is.order...)
	sort.Strings(paths)
	lines := make([]string, 0, len(paths))
	for _, p := range paths {
		if alias := is.byPath[p]; alias != "" {
			lines = append(lines, "\t"+alias+" \""+p+"\"")
		} else {
			lines = append(lines, "\t\""+p+"\"")
		}
	}
	return "import (\n" + strings.Join(lines, "\n") + "\n)"
}

// renderer 单个源文件上下文下的表达式渲染器。
// OwnerAlias：Enterpoint 包在目标文件中的导入别名（"" = 本地，table 模式）。
type renderer struct {
	pkg        *Package
	ownerAlias string
	src        *File
	table      bool
	is         *importSet
}

var builtinIdents = map[string]bool{
	"string": true, "bool": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
	"byte": true, "rune": true,
	"any": true, "error": true,
}

// expr 渲染类型表达式。
func (rd *renderer) expr(x ast.Expr) (string, error) {
	switch t := x.(type) {
	case *ast.Ident:
		if builtinIdents[t.Name] {
			return t.Name, nil
		}
		if rd.pkg.aliases[t.Name] == "any" {
			return "any", nil
		}
		if rd.table {
			return t.Name, nil
		}
		rd.is.add(rd.pkg.ImportPath, rd.ownerAlias)
		return rd.ownerAlias + "." + t.Name, nil
	case *ast.SelectorExpr:
		pkgIdent, ok := t.X.(*ast.Ident)
		if !ok {
			return "", fmt.Errorf("不支持的选择器表达式 %v", t.X)
		}
		if p, ok2 := rd.src.byAlias[pkgIdent.Name]; ok2 {
			if rd.table && p == rd.pkg.ImportPath {
				return t.Sel.Name, nil
			}
			rd.is.add(p, pkgIdent.Name)
			return pkgIdent.Name + "." + t.Sel.Name, nil
		}
		p, ok2 := rd.src.byBase[pkgIdent.Name]
		if !ok2 {
			return "", fmt.Errorf("无法解析包限定符 %q（%s）", pkgIdent.Name, rd.src.Filename)
		}
		if rd.table && p == rd.pkg.ImportPath {
			return t.Sel.Name, nil
		}
		rd.is.add(p, "")
		return pkgIdent.Name + "." + t.Sel.Name, nil
	case *ast.StarExpr:
		inner, err := rd.expr(t.X)
		return "*" + inner, err
	case *ast.ArrayType:
		if t.Len != nil {
			return "", fmt.Errorf("不支持定长数组类型")
		}
		inner, err := rd.expr(t.Elt)
		return "[]" + inner, err
	case *ast.MapType:
		k, err := rd.expr(t.Key)
		if err != nil {
			return "", err
		}
		v, err := rd.expr(t.Value)
		return "map[" + k + "]" + v, err
	case *ast.IndexExpr:
		base, err := rd.expr(t.X)
		if err != nil {
			return "", err
		}
		arg, err := rd.expr(t.Index)
		return base + "[" + arg + "]", err
	case *ast.IndexListExpr:
		base, err := rd.expr(t.X)
		if err != nil {
			return "", err
		}
		var args []string
		for _, ix := range t.Indices {
			a, err := rd.expr(ix)
			if err != nil {
				return "", err
			}
			args = append(args, a)
		}
		return base + "[" + strings.Join(args, ", ") + "]", nil
	case *ast.ParenExpr:
		return rd.expr(t.X)
	case *ast.InterfaceType:
		if len(t.Methods.List) == 0 {
			return "interface{}", nil
		}
		return "", fmt.Errorf("不支持非空 interface 类型")
	default:
		return "", fmt.Errorf("不支持的类型表达式 %T", x)
	}
}

// ---- 字段分类（绑定器发射的依据，语义对齐 v0.1 SetRaw/SetSliceValue/BindMultipart）----

type fieldClass int

const (
	classScalar    fieldClass = iota // string/bool/各宽度数值/time.Time
	classPtrScalar                   // *scalar
	classSlice                       // []scalar
	classFile                        // *hinge.FileHeader
	classFileSlice                   // []*hinge.FileHeader
)

// Field 展平后的绑定字段。
type Field struct {
	GoName   string
	Access   string   // 赋值路径（含 "v." 前缀，含内嵌链）
	TypeExpr ast.Expr // scalar/slice 元素/ptr 目标的类型表达式
	SrcFile  *File
	BaseKind string // 标量种类（"int"/"string"/"time.Time"...，零值与 Parse 泛型实参用）
	In       string // path/header/cookie/query/form；""=body
	Source   string // 外部参数名（来源标签值 / json 名 / 字段名）
	JSONName string // body 外部名（json 标签）
	Def      string // default 标签原值
	Required bool
	Class    fieldClass
}

// allocInfo 指针内嵌结构体的分配语句（绑定器字段赋值前执行）。
type allocInfo struct {
	Access   string // 如 "v.Base"
	TypeName string // 内嵌结构体本地名
	SrcFile  *File
}

// fieldSet 展平后的字段集合。
type fieldSet struct {
	Fields []Field
	Allocs []allocInfo
}

const hingeImportPath = "github.com/EdSan845D/oapi-hinge/hinge"

// isHingeSelector 判断 x 是否 <hinge 包>.<name> 选择器。
func isHingeSelector(x ast.Expr, file *File, name string) bool {
	se, ok := x.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := se.X.(*ast.Ident)
	if !ok || se.Sel.Name != name {
		return false
	}
	p, ok2 := file.importPathOf(id.Name)
	return ok2 && p == hingeImportPath
}

// isScalar 判断表达式是否为可解析标量（含 time.Time）；kind 返回种类名。
func isScalar(x ast.Expr, file *File) (bool, string) {
	switch t := x.(type) {
	case *ast.Ident:
		switch t.Name {
		case "any", "error":
			return false, ""
		case "byte":
			return true, "uint8"
		case "rune":
			return true, "int32"
		}
		if builtinIdents[t.Name] {
			return true, t.Name
		}
		return false, ""
	case *ast.SelectorExpr:
		id, ok := t.X.(*ast.Ident)
		if !ok || t.Sel.Name != "Time" {
			return false, ""
		}
		p, ok2 := file.importPathOf(id.Name)
		if ok2 && p == "time" {
			return true, "time.Time"
		}
		return false, ""
	}
	return false, ""
}

// classify 字段类型分类；指针/切片取目标元素表达式。
func classify(x ast.Expr, file *File) (fieldClass, ast.Expr, string, error) {
	switch t := x.(type) {
	case *ast.StarExpr:
		if ok, kind := isScalar(t.X, file); ok {
			return classPtrScalar, t.X, kind, nil
		}
		if isHingeSelector(t.X, file, "FileHeader") {
			return classFile, t.X, "", nil
		}
		return 0, nil, "", fmt.Errorf("不支持的指针字段类型（仅标量 / hinge.FileHeader）")
	case *ast.ArrayType:
		if t.Len != nil {
			return 0, nil, "", fmt.Errorf("不支持定长数组字段")
		}
		if ok, kind := isScalar(t.Elt, file); ok {
			return classSlice, t.Elt, kind, nil
		}
		if se, ok := t.Elt.(*ast.StarExpr); ok && isHingeSelector(se.X, file, "FileHeader") {
			return classFileSlice, se.X, "", nil
		}
		return 0, nil, "", fmt.Errorf("不支持的切片字段类型（仅标量 / []*hinge.FileHeader）")
	default:
		if ok, kind := isScalar(x, file); ok {
			return classScalar, x, kind, nil
		}
		return 0, nil, "", fmt.Errorf("不支持的绑定字段类型（支持的形态：标量 / *标量 / []标量 / *hinge.FileHeader / []*hinge.FileHeader）")
	}
}

// tagGet 取结构体标签首个逗号前的值。
func tagGet(tag reflect.StructTag, key string) (string, bool) {
	v, ok := tag.Lookup(key)
	if !ok || v == "" {
		return "", false
	}
	return strings.Split(v, ",")[0], true
}

// tagRequired 判定必填（binding / validate 双标签兼容）。
func tagRequired(tag reflect.StructTag) bool {
	return strings.Contains(tag.Get("binding"), "required") ||
		strings.Contains(tag.Get("validate"), "required")
}

// resolveFields 展平结构体字段（内嵌结构体递归；语义对齐 v0.1 ParseFields + FieldMeta.Source 优先级）。
func resolveFields(pkg *Package, typeName, accessPrefix string, depth int) (*fieldSet, error) {
	if depth > 8 {
		return nil, fmt.Errorf("内嵌结构体层级过深: %s", typeName)
	}
	si, ok := pkg.structOf(typeName)
	if !ok {
		return nil, fmt.Errorf("类型 %s 不在扫描包内（v0.2 约束：Q/B 必须是扫描目录内同包结构体）", typeName)
	}
	fs := &fieldSet{}
	for _, sf := range si.st.Fields.List {
		if len(sf.Names) == 0 {
			// 内嵌结构体：递归展平
			embedded := sf.Type
			isPtr := false
			if se, ok := embedded.(*ast.StarExpr); ok {
				isPtr = true
				embedded = se.X
			}
			id, ok := embedded.(*ast.Ident)
			if !ok {
				return nil, fmt.Errorf("不支持的内嵌类型（需为包内具名结构体）")
			}
			if !ast.IsExported(id.Name) {
				return nil, fmt.Errorf("内嵌结构体 %s 未导出：生成代码无法跨包赋值（v0.2 约束：内嵌必须为导出类型）", id.Name)
			}
			if _, ok := pkg.structOf(id.Name); !ok {
				return nil, fmt.Errorf("内嵌结构体 %s 不在同包（v0.2 约束：内嵌必须为同包类型）", id.Name)
			}
			if isPtr {
				fs.Allocs = append(fs.Allocs, allocInfo{
					Access:   "v." + accessPrefix + id.Name,
					TypeName: id.Name,
					SrcFile:  si.file,
				})
			}
			sub, err := resolveFields(pkg, id.Name, accessPrefix+id.Name+".", depth+1)
			if err != nil {
				return nil, err
			}
			fs.Fields = append(fs.Fields, sub.Fields...)
			fs.Allocs = append(fs.Allocs, sub.Allocs...)
			continue
		}
		var tag reflect.StructTag
		if sf.Tag != nil {
			tag = reflect.StructTag(strings.Trim(sf.Tag.Value, "`"))
		}
		for _, n := range sf.Names {
			if !n.IsExported() {
				continue // 与 v0.1 一致：未导出字段跳过
			}
			fd := Field{
				GoName:   n.Name,
				Access:   "v." + accessPrefix + n.Name,
				SrcFile:  si.file,
				JSONName: mustJSONName(tag, n.Name),
			}
			// 来源标签与优先级（path > header > cookie > query > form）
			for _, key := range []string{"path", "header", "cookie", "query", "form"} {
				if v, ok := tagGet(tag, key); ok {
					fd.In = key
					fd.Source = v
					break
				}
			}
			fd.Source = firstNonEmpty(fd.Source, fd.JSONName)
			fd.Def, _ = tagGet(tag, "default")
			fd.Required = tagRequired(tag)
			cls, elem, kind, err := classify(sf.Type, si.file)
			if err != nil {
				return nil, fmt.Errorf("字段 %s.%s: %w", typeName, n.Name, err)
			}
			fd.Class = cls
			fd.TypeExpr = elem
			fd.BaseKind = kind
			fs.Fields = append(fs.Fields, fd)
		}
	}
	return fs, nil
}

func mustJSONName(tag reflect.StructTag, fallback string) string {
	if v, ok := tagGet(tag, "json"); ok && v != "-" {
		return v
	}
	return fallback
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
