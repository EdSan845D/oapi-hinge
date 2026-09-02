package gen

import (
	"fmt"
	"go/ast"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EndpointIR：一个端点的中间表示（注解 + 签名 + 字段全部解析后的产物），
// 发射器只消费 IR，不再接触 AST。
type EndpointIR struct {
	Owner    string
	Pkg      *Package
	Handler  string
	Method   string
	RelPath  string
	FullPath string

	Summary     string
	Description string
	Tags        []string
	Status      int // 0 → 200
	Deprecated  bool
	Envelope    string
	Auth        string
	Limit       string
	TimeoutStr  string // oapi:timeout 原值（发射 hinge.MustDuration("<原值>")）
	Middleware  []string

	HasQ  bool
	QName string
	QSet  *fieldSet

	HasB    bool
	BName   string
	BSet    *fieldSet
	BodyKind string // json / raw / multipart（HasB 时有效）

	InTransformQ    bool
	InTransformQPtr bool
	ValidateQ       bool
	ValidateQPtr    bool
	InTransformB    bool
	InTransformBPtr bool
	ValidateB       bool
	ValidateBPtr    bool
	TwoArg          bool // func(ctx, Q) 简式

	// 发射期回填：去重后的绑定器函数名（空 = 无绑定器）
	qBinder string
	bBinder string

	RExpr    ast.Expr
	RSrcFile *File
}

var (
	pathParamRe = regexp.MustCompile(`\{([^}/]+)\}`)
	httpMethods = map[string]bool{
		"GET": true, "POST": true, "PUT": true, "PATCH": true,
		"DELETE": true, "HEAD": true, "OPTIONS": true,
	}
)

// structAnn 结构体级注解。
type structAnn struct {
	Prefix     string
	Tags       []string
	Auth       string
	Limit      string
	TimeoutStr string
	Middleware []string
}

// irBuilder IR 构建上下文（错误聚合，全部解析完统一报告）。
type irBuilder struct {
	packages []*Package
	eps      []*EndpointIR
	errs     []string
}

func (b *irBuilder) errf(format string, args ...any) {
	b.errs = append(b.errs, fmt.Sprintf(format, args...))
}

// buildIR 从扫描包构建全部端点 IR（含完整校验）。
func buildIR(packages []*Package) ([]*EndpointIR, error) {
	b := &irBuilder{packages: packages}
	for _, pkg := range packages {
		b.buildPackage(pkg)
	}
	// 全局查重：method+path
	seen := map[string]string{}
	for _, ep := range b.eps {
		key := ep.Method + " " + ep.FullPath
		if prev, dup := seen[key]; dup {
			b.errf("路由冲突：%s 同时由 %s 与 %s 声明", key, prev, ep.Owner+"."+ep.Handler)
			continue
		}
		seen[key] = ep.Owner + "." + ep.Handler
	}
	if len(b.errs) > 0 {
		return nil, fmt.Errorf("生成期校验失败（%d 项）：\n  - %s", len(b.errs), strings.Join(b.errs, "\n  - "))
	}
	sort.Slice(b.eps, func(i, j int) bool {
		if b.eps[i].Pkg.Dir != b.eps[j].Pkg.Dir {
			return b.eps[i].Pkg.Dir < b.eps[j].Pkg.Dir
		}
		if b.eps[i].Owner != b.eps[j].Owner {
			return b.eps[i].Owner < b.eps[j].Owner
		}
		return b.eps[i].Handler < b.eps[j].Handler
	})
	return b.eps, nil
}

func (b *irBuilder) buildPackage(pkg *Package) {
	// 找出全部 Enterpoint：拥有 oapi:route 方法的接收者结构体
	owners := map[string]bool{}
	for recv, mds := range pkg.methods {
		for _, md := range mds {
			kv, _ := annotations(md.Doc)
			for _, pair := range kv {
				if pair[0] == "route" {
					owners[recv] = true
					break
				}
			}
		}
	}
	names := make([]string, 0, len(owners))
	for name := range owners {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, owner := range names {
		b.buildOwner(pkg, owner)
	}
}

// buildOwner 构建一个 Enterpoint 的全部端点。
func (b *irBuilder) buildOwner(pkg *Package, owner string) {
	if !ast.IsExported(owner) {
		b.errf("Enterpoint %s 未导出：生成注册代码无法引用", owner)
		return
	}
	// 结构体级注解
	sa := structAnn{}
	if doc := pkg.structDoc(owner); doc != nil {
		kv, _ := annotations(doc)
		for _, pair := range kv {
			key, value := pair[0], pair[1]
			switch key {
			case "prefix":
				sa.Prefix = value
			case "tag":
				if value != "" {
					sa.Tags = append(sa.Tags, value)
				}
			case "auth":
				sa.Auth = value
			case "limit":
				sa.Limit = value
			case "timeout":
				sa.TimeoutStr = value
			case "middleware":
				if value != "" {
					sa.Middleware = append(sa.Middleware, value)
				}
			default:
				b.errf("Enterpoint %s：未知 struct 级注解 oapi:%s（允许 prefix/tag/auth/limit/timeout/middleware）", owner, key)
				return
			}
		}
	}
	if sa.TimeoutStr != "" {
		if _, err := time.ParseDuration(sa.TimeoutStr); err != nil {
			b.errf("Enterpoint %s：oapi:timeout 值 %q 非法（需 time.ParseDuration 形态，如 5s）", owner, sa.TimeoutStr)
			return
		}
	}
	for _, md := range pkg.methodsOf(owner) {
		kv, docLines := annotations(md.Doc)
		route := ""
		ma := map[string]string{}
		var mTags, mMiddleware []string
		deprecated := false
		hasRoute := false
		for _, pair := range kv {
			key, value := pair[0], pair[1]
			switch key {
			case "route":
				route = value
				hasRoute = true
			case "tag":
				if value != "" {
					mTags = append(mTags, value)
				}
			case "middleware":
				if value != "" {
					mMiddleware = append(mMiddleware, value)
				}
			case "auth":
				ma["auth"] = value
			case "limit":
				ma["limit"] = value
			case "timeout":
				ma["timeout"] = value
			case "status":
				ma["status"] = value
			case "envelope":
				ma["envelope"] = value
			case "deprecated":
				deprecated = true
			default:
				b.errf("%s.%s：未知方法级注解 oapi:%s（允许 route/tag/auth/limit/timeout/status/deprecated/envelope/middleware）", owner, md.Name.Name, key)
			}
		}
		if !hasRoute {
			continue
		}
		ep := &EndpointIR{
			Owner:      owner,
			Pkg:        pkg,
			Handler:    md.Name.Name,
			Summary:    firstNonEmpty(docLines...),
			Tags:       append(append([]string{}, sa.Tags...), mTags...),
			Deprecated: deprecated,
			Envelope:   ma["envelope"],
			Auth:       firstNonEmpty(ma["auth"], sa.Auth),
			Limit:      firstNonEmpty(ma["limit"], sa.Limit),
			TimeoutStr: firstNonEmpty(ma["timeout"], sa.TimeoutStr),
			Middleware: append(append([]string{}, sa.Middleware...), mMiddleware...),
		}
		if len(docLines) > 1 {
			ep.Description = strings.Join(docLines[1:], "\n")
		}
		if ep.Summary == "" {
			// 无文档注释时用方法名兜底（openapi 端不再有 handler 注释来源）
			ep.Summary = md.Name.Name
		}
		if v := ma["status"]; v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				b.errf("%s.%s：oapi:status 值 %q 非整数", owner, md.Name.Name, v)
				continue
			}
			ep.Status = n
		}
		if ep.TimeoutStr != "" {
			if _, err := time.ParseDuration(ep.TimeoutStr); err != nil {
				b.errf("%s.%s：oapi:timeout 值 %q 非法", owner, md.Name.Name, ep.TimeoutStr)
				continue
			}
		}
		if !b.buildRoute(ep, md, route, sa.Prefix) {
			continue
		}
		if !b.buildSignature(ep, md) {
			continue
		}
		b.eps = append(b.eps, ep)
	}
}

// buildRoute 解析 oapi:route 值并拼装完整路径。
func (b *irBuilder) buildRoute(ep *EndpointIR, md *ast.FuncDecl, route, prefix string) bool {
	pos := ep.Owner + "." + ep.Handler
	parts := strings.Fields(route)
	if len(parts) == 0 || len(parts) > 2 {
		b.errf("%s：oapi:route 值 %q 非法（形态：\"<METHOD> <相对路径>\"，路径可省略表示组根）", pos, route)
		return false
	}
	method := strings.ToUpper(parts[0])
	if !httpMethods[method] {
		b.errf("%s：未知 HTTP 方法 %q", pos, parts[0])
		return false
	}
	rel := ""
	if len(parts) == 2 {
		rel = parts[1]
	}
	// 防呆：方法级路径相对 prefix。写了全路径（与 prefix 重叠）是常见笔误，直接报错并给出改法
	if prefix != "" && (rel == prefix || strings.HasPrefix(rel, prefix+"/")) {
		b.errf("%s：oapi:route 路径 %q 已包含组前缀 %q——方法级路径相对 prefix，请改为 %q，或移除 oapi:prefix 改用全路径风格",
			pos, rel, prefix, strings.TrimPrefix(strings.TrimPrefix(rel, prefix), "/"))
		return false
	}
	ep.Method = method
	ep.RelPath = rel
	ep.FullPath = joinRoutePath(prefix, rel)
	return true
}

// joinRoutePath 组前缀 + 相对路径（清理双斜杠；组根路由保持组前缀）。
func joinRoutePath(prefix, rel string) string {
	if rel == "" {
		rel = "/"
	}
	if prefix == "" {
		prefix = "/"
	}
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	p := strings.TrimSuffix(prefix, "/") + rel
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if p != "/" {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// buildSignature 解析端点方法签名（func(ctx[, Q[, B]]) (R, error)）与字段集。
func (b *irBuilder) buildSignature(ep *EndpointIR, md *ast.FuncDecl) bool {
	pos := ep.Owner + "." + ep.Handler
	srcFile := ep.Pkg.fileOf(md)
	ft := md.Type
	if ft.Results == nil {
		b.errf("%s：签名结果必须为 (R, error)", pos)
		return false
	}
	results := flattenFields(ft.Results)
	if len(results) != 2 {
		b.errf("%s：签名结果必须为 (R, error)，实际 %d 个", pos, len(results))
		return false
	}
	if id, ok := results[1].Type.(*ast.Ident); !ok || id.Name != "error" {
		b.errf("%s：第二个结果必须为 error", pos)
		return false
	}
	ep.RExpr = results[0].Type
	ep.RSrcFile = srcFile
	params := flattenFields(ft.Params)
	if len(params) < 2 || len(params) > 3 {
		b.errf("%s：签名必须为 func(ctx context.Context, Q[, B]) (R, error)，实际 %d 个参数", pos, len(params))
		return false
	}
	if !isContextParam(params[0].Type, ep.Pkg) {
		b.errf("%s：第一个参数必须为 context.Context", pos)
		return false
	}
	ep.TwoArg = len(params) == 2
	// ---- Q ----
	qExpr := params[1].Type
	qName, qAny, err := classifyParam(qExpr, ep.Pkg, "Q")
	if err != nil {
		b.errf("%s：%v", pos, err)
		return false
	}
	if !qAny {
		ep.HasQ = true
		ep.QName = qName
		fs, ferr := resolveFields(ep.Pkg, qName, "", 0)
		if ferr != nil {
			b.errf("%s：%v", pos, ferr)
			return false
		}
		ep.QSet = fs
		b.checkPathParams(ep)
		ep.InTransformQ, ep.InTransformQPtr = methodShape(ep.Pkg, qName, "InTransform", true)
		ep.ValidateQ, ep.ValidateQPtr = methodShape(ep.Pkg, qName, "Validate", false)
	}
	// ---- B ----
	if len(params) == 3 {
		bExpr := params[2].Type
		bName, bAny, err := classifyParam(bExpr, ep.Pkg, "B")
		if err != nil {
			b.errf("%s：%v", pos, err)
			return false
		}
		if !bAny {
			ep.HasB = true
			ep.BName = bName
			switch {
			case isHingeSelector(bExpr, srcFile, "RawBody"):
				ep.BodyKind = "raw"
			default:
				fs, ferr := resolveFields(ep.Pkg, bName, "", 0)
				if ferr != nil {
					b.errf("%s：%v", pos, ferr)
					return false
				}
				ep.BSet = fs
				ep.BodyKind = "json"
				for _, f := range fs.Fields {
					if f.Class == classFile || f.Class == classFileSlice {
						ep.BodyKind = "multipart"
						break
					}
				}
				if ep.BodyKind == "multipart" {
					for _, f := range fs.Fields {
						if (f.Class == classFile || f.Class == classFileSlice) && f.In != "form" {
							b.errf("%s：multipart 字段 %s 必须声明 form 标签（否则文件静默丢失）", pos, f.GoName)
						}
					}
				}
			}
			ep.InTransformB, ep.InTransformBPtr = methodShape(ep.Pkg, bName, "InTransform", true)
			ep.ValidateB, ep.ValidateBPtr = methodShape(ep.Pkg, bName, "Validate", false)
		}
	}
	// 2 参简式（无 B）= 端点声明无 body，任意方法均可用
	//（POST/PATCH 无 body 的触发类动作同样合法；文档侧 BType 未填即无 requestBody）
	return true
}

// classifyParam 解析 Q/B 参数类型：any/NoReq → 占位；hinge.RawBody → raw 标记；
// 同包结构体 → 类型名。
func classifyParam(x ast.Expr, pkg *Package, role string) (name string, isAny bool, err error) {
	switch t := x.(type) {
	case *ast.Ident:
		if t.Name == "any" {
			return "", true, nil
		}
		if pkg.aliases[t.Name] == "any" {
			return "", true, nil
		}
		if _, ok := pkg.structOf(t.Name); ok {
			return t.Name, false, nil
		}
		return "", false, fmt.Errorf("%s 参数类型 %q 既非 any 占位、也非扫描包内结构体", role, t.Name)
	case *ast.SelectorExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			if p, ok2 := pkg.importPathOfAny(id.Name); ok2 && p == hingeImportPath {
				switch t.Sel.Name {
				case "NoReq":
					return "", true, nil
				case "RawBody":
					return "RawBody", false, nil // 特殊形态，调用方识别
				}
			}
		}
		return "", false, fmt.Errorf("%s 参数必须为同包结构体、any/NoReq 或 hinge.RawBody（跨包结构体无法生成绑定器）", role)
	}
	return "", false, fmt.Errorf("%s 参数类型形态不支持", role)
}

// checkPathParams 路径参数与 Q 字段的双向一致性校验。
func (b *irBuilder) checkPathParams(ep *EndpointIR) {
	found := pathParamRe.FindAllStringSubmatch(ep.FullPath, -1)
	inQ := map[string]bool{}
	for _, f := range ep.QSet.Fields {
		if f.In == "path" {
			inQ[f.Source] = true
		}
	}
	if len(found) == 0 {
		for name := range inQ {
			b.errf("%s.%s：Q 字段声明了 path:%q 但路由 %s 未包含 {%s}", ep.Owner, ep.Handler, name, ep.FullPath, name)
		}
		return
	}
	seen := map[string]bool{}
	for _, m := range found {
		name := m[1]
		if seen[name] {
			b.errf("%s.%s：路径 %s 重复参数 {%s}", ep.Owner, ep.Handler, ep.FullPath, name)
			continue
		}
		seen[name] = true
		if !inQ[name] {
			b.errf("%s.%s：路径参数 {%s} 在 Q 结构体中没有对应的 path:%q 字段", ep.Owner, ep.Handler, name, name)
		}
	}
	for name := range inQ {
		if !seen[name] {
			b.errf("%s.%s：Q 字段声明了 path:%q 但路由 %s 未包含 {%s}", ep.Owner, ep.Handler, name, ep.FullPath, name)
		}
	}
}

// methodShape 探测类型是否实现指定方法（InTransform：1 参 context + 1 结果 error；
// Validate：0 参 + 1 结果 error）。返回 (存在, 指针接收者)。
func methodShape(pkg *Package, typeName, name string, withCtx bool) (bool, bool) {
	for _, md := range pkg.methodsOf(typeName) {
		if md.Name.Name != name {
			continue
		}
		if md.Type.Results == nil || len(flattenFields(md.Type.Results)) != 1 {
			continue
		}
		if id, ok := flattenFields(md.Type.Results)[0].Type.(*ast.Ident); !ok || id.Name != "error" {
			continue
		}
		params := flattenParams(md.Type.Params)
		if withCtx {
			if len(params) != 1 {
				continue
			}
		} else if len(params) != 0 {
			continue
		}
		ptrRecv := false
		if len(md.Recv.List) > 0 {
			_, ptrRecv = md.Recv.List[0].Type.(*ast.StarExpr)
		}
		return true, ptrRecv
	}
	return false, false
}

func isContextParam(x ast.Expr, pkg *Package) bool {
	se, ok := x.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := se.X.(*ast.Ident)
	if !ok || se.Sel.Name != "Context" {
		return false
	}
	p, ok2 := pkg.importPathOfAny(id.Name)
	return ok2 && p == "context"
}

// importPathOfAny 跨包全部文件解析包限定符。
func (p *Package) importPathOfAny(alias string) (string, bool) {
	for _, f := range p.Files {
		if path, ok := f.importPathOf(alias); ok {
			return path, true
		}
	}
	return "", false
}

// fileOf 方法声明所在源文件。
func (p *Package) fileOf(md *ast.FuncDecl) *File {
	for _, f := range p.Files {
		for _, decl := range f.decls {
			if decl == md {
				return f
			}
		}
	}
	return nil
}

// flattenFields 把参数/结果列表展平为逐个条目（a, b int 分组展开）。
func flattenFields(fl *ast.FieldList) []*ast.Field {
	var out []*ast.Field
	for _, f := range fl.List {
		if len(f.Names) == 0 {
			out = append(out, f)
			continue
		}
		for range f.Names {
			out = append(out, f)
		}
	}
	return out
}

func flattenParams(fl *ast.FieldList) []*ast.Field { return flattenFields(fl) }

// IsBodyMethod 是否携带请求体的方法（与 hinge 包语义一致）。
func IsBodyMethod(method string) bool {
	return method == "POST" || method == "PUT" || method == "PATCH"
}
