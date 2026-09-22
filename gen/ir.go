package gen

import (
	"fmt"
	"go/ast"
	"os"
	"path"
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
	// Prefix oapi:prefix 注解原值（挂载链展平时作为节点子树基座前缀）。
	Prefix string
	// Parent oapi:parent 注解解析出的父 Enterpoint 结构体名（空 = 根挂载）。
	// 同 owner 全端点共享；展平后祖先链前缀烘焙进 FullPath、祖先组级注解
	// 中间件/拦截器进 MountMWs / MountICs。
	Parent string

	Summary     string
	Description string
	Tags        []string
	Status      int // 0 → 200
	Deprecated  bool
	Envelope    string
	TimeoutStr  string // oapi:timeout 原值（发射 hinge.MustDuration("<原值>")）

	// AnnoMWs 方法级 oapi:middleware 注解解析出的源码引用：发射为路由级直挂参数，
	// 位于内核包装器之前，编译期类型校验。必须为框架原生中间件。
	AnnoMWs []MWRef
	// GroupMWs 结构体级 oapi:middleware 注解解析出的源码引用：发射为组级中间件
	//（scoped Group("", mws...)，仅作用于本 Enterpoint 的路由，不污染传入路由）。
	// 组级没有端点上下文，引用必须为框架原生中间件。
	GroupMWs []MWRef
	// AnnoICs 方法级 oapi:interceptor 注解解析出的源码引用：发射为 HandleWith
	// 的 extra 实参（内核链），需为 hinge.Interceptor 签名。
	AnnoICs []MWRef
	// GroupICs 结构体级 oapi:interceptor 注解解析出的源码引用：owner 全端点
	// 注入 extra（先于方法级），需为 hinge.Interceptor 签名。
	GroupICs []MWRef
	// ConfigICs EntryPointConfig.Interceptors 运行时值反射出的源码引用
	//（per-ep 补充，owner 全端点 extra，先于结构体级注解；不沿 parent 链继承）。
	ConfigICs []MWRef
	// MountMWs / MountICs oapi:parent 挂载链沿祖先（先根后叶）继承的 struct 级
	// oapi:middleware / oapi:interceptor 源码引用（不含自身）；发射时排在
	// RouteMWs / ConfigICs 之前。仅 struct 级注解沿链继承，Config 补充不沿链。
	MountMWs []MWRef
	MountICs []MWRef

	HasQ  bool
	QName string
	QSet  *fieldSet

	HasB     bool
	BName    string
	BSet     *fieldSet
	BodyKind string // json / raw / multipart / form（HasB 时有效；form = application/x-www-form-urlencoded）

	// RouteMWs EntryPointConfig.Middlewares 运行时值反射出的源码引用（per-ep 补充，
	// 不沿 parent 链继承）：全部为框架原生中间件，发射为组级直挂；内核拦截器
	// 走 Interceptors → ConfigICs。RouteMWImports 为对应 import 路径。
	RouteMWs       []RouteMWRef
	RouteMWImports []string

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

// bodyKindOf 由展平后的 B 字段集判定 body kind（不含校验）：
//   - 含文件字段 → multipart（value/文件字段全部要求 form 标签，由调用方校验）
//   - 全部字段 form 标签（无文件字段）→ form（application/x-www-form-urlencoded）
//   - 其余（含字段标签混排）→ json（保持 JSON 语义）
func bodyKindOf(fs *fieldSet) string {
	hasFile := false
	allForm := len(fs.Fields) > 0
	for _, f := range fs.Fields {
		if f.Class == classFile || f.Class == classFileSlice {
			hasFile = true
		}
		if f.In != "form" {
			allForm = false
		}
	}
	switch {
	case hasFile:
		return "multipart"
	case allForm:
		return "form"
	default:
		return "json"
	}
}

// structAnn 结构体级注解。
type structAnn struct {
	Prefix     string
	Parent     string // oapi:parent：父 Enterpoint 结构体名（纯名，子声明式挂载；空 = 根挂载）
	Tags       []string
	TimeoutStr string
	MWs        []string // oapi:middleware：框架原生中间件 → 组级直挂（沿 parent 链继承给后代）
	ICs        []string // oapi:interceptor：内核拦截器 → owner 全端点 extra（沿 parent 链继承给后代）
}

// MWRef 路由级源码引用中间件（oapi:middleware "pkg.Func" 形态）。
// Qualifier 为发射时使用的包别名（沿用注解限定符；完整路径形态取基名），
// 发射时经 pkgAlias 重映射防冲突；Import 为解析出的完整 import 路径。
type MWRef struct {
	Qualifier string
	Name      string
	Import    string
}

// RouteMWRef EntryPointConfig.Middlewares 运行时值反射出的源码引用。
// gen.Run 与 generate.go 同进程，运行时值类型可精确判定：
// 仅允许框架原生中间件（hinge.Interceptor 值报错指引到 Interceptors 字段）。
type RouteMWRef struct {
	Ref    string // 限定名引用，如 middleware.Auth
	Import string // 完整 import 路径
}

// resolveAnnoRefs 把注解值解析为源码引用（严格模式）：解析失败即生成期报错。
// 注册表已删除，不存在「未解析 → 内核注册名」的回落——回落只会静默丢失。
// kind 用于报错定位（oapi:middleware / oapi:interceptor），pos 为声明位置。
func (b *irBuilder) resolveAnnoRefs(pkg *Package, scanned map[string]string, names []string, kind, pos string) []MWRef {
	refs := make([]MWRef, 0, len(names))
	for _, name := range names {
		ref, ok := resolveMWRef(pkg, scanned, name)
		if !ok {
			b.errf("%s：%s %q 无法解析为源码引用（限定符须在端点包 import 表/被扫描包名中，或完整 import 路径形态；字符串注册表已移除，不支持裸名）", pos, kind, name)
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}

// resolveMWRef 把注解值解析为路由级源码引用。依次尝试：
//  1. 端点包自身 import 表（显式别名 / 包基名）；
//  2. 被扫描包的包名（中间件包加入 scan 即可用短名引用；同名多包记 "" 表歧义，不解析）；
//  3. 完整 import 路径形态（限定符含 "/"，别名取路径基名）。
func resolveMWRef(pkg *Package, scanned map[string]string, name string) (MWRef, bool) {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 || dot >= len(name)-1 {
		return MWRef{}, false
	}
	qualifier, fn := name[:dot], name[dot+1:]
	if imp, ok := pkg.importPathOfAny(qualifier); ok {
		return MWRef{Qualifier: qualifier, Name: fn, Import: imp}, true
	}
	if imp, ok := scanned[qualifier]; ok && imp != "" {
		return MWRef{Qualifier: qualifier, Name: fn, Import: imp}, true
	}
	if strings.Contains(qualifier, "/") {
		return MWRef{Qualifier: path.Base(qualifier), Name: fn, Import: qualifier}, true
	}
	return MWRef{}, false
}

// DocMWRefs 文档侧中间件引用名单：源码引用全限定形态
// （importPath.FuncName，与反射派生的函数名一致，供 openapi 钩子配对
// 与 security scheme 推导；框架中间件与内核拦截器两类都进，行为一致）。
// 顺序：EntryPointConfig 注入 → 结构体级 → 方法级，去重保序。
func (ep *EndpointIR) DocMWRefs() []string {
	out := make([]string, 0, len(ep.RouteMWs)+len(ep.ConfigICs)+len(ep.GroupMWs)+len(ep.GroupICs)+len(ep.AnnoMWs)+len(ep.AnnoICs))
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, r := range ep.RouteMWs {
		if r.Import != "" {
			if dot := strings.LastIndex(r.Ref, "."); dot > 0 {
				add(r.Import + r.Ref[dot:])
				continue
			}
		}
		add(r.Ref)
	}
	for _, r := range ep.ConfigICs {
		add(r.Import + "." + r.Name)
	}
	for _, r := range ep.GroupMWs {
		add(r.Import + "." + r.Name)
	}
	for _, r := range ep.GroupICs {
		add(r.Import + "." + r.Name)
	}
	for _, r := range ep.AnnoMWs {
		add(r.Import + "." + r.Name)
	}
	for _, r := range ep.AnnoICs {
		add(r.Import + "." + r.Name)
	}
	return out
}

// funcIdOf 端点对应的 FuncIdentity 键形态：包级所有者 = "pkg.Handler"，
// 结构体所有者 = "pkg.Owner.Handler"（与 runtime.FuncForPC 派生的
// FuncIdentity 字符串对齐，如 "eps.SystemEp.Health"）。
func funcIdOf(ep *EndpointIR) string {
	if strings.HasPrefix(ep.Owner, PKGFlag) {
		return ep.Pkg.Name + "." + ep.Handler
	}
	return ep.Pkg.Name + "." + ep.Owner + "." + ep.Handler
}

// applyRouteMeta 把 FuncDecls.RouteMeta 按字段级语义合并进端点 IR：
// 非零/非 nil 字段才覆盖注解值（零值保持注解不变）；Method/Path 为预留字段，
// 路由以注解为唯一事实源，不参与覆写。返回被覆写字段的可读描述列表。
func applyRouteMeta(ep *EndpointIR, rm RouteMeta) []string {
	var changed []string
	if rm.Summary != "" {
		ep.Summary = rm.Summary
		changed = append(changed, "summary")
	}
	if rm.Description != "" {
		ep.Description = rm.Description
		changed = append(changed, "description")
	}
	if len(rm.Tags) > 0 {
		ep.Tags = append([]string{}, rm.Tags...)
		changed = append(changed, "tags")
	}
	if rm.DefaultStatusCode != 0 {
		ep.Status = rm.DefaultStatusCode
		changed = append(changed, fmt.Sprintf("status=%d", rm.DefaultStatusCode))
	}
	if rm.Envelope != "" {
		ep.Envelope = rm.Envelope
		changed = append(changed, "envelope="+rm.Envelope)
	}
	if rm.Deprecated != nil {
		ep.Deprecated = *rm.Deprecated
		changed = append(changed, fmt.Sprintf("deprecated=%v", *rm.Deprecated))
	}
	return changed
}

// irBuilder IR 构建上下文（错误聚合，全部解析完统一报告）。
type irBuilder struct {
	packages []*Package
	// scanned 被扫描包的 包名 → import 路径（唯一才登记；同名多包记 "" 表歧义），
	// 供 oapi:middleware 路由级引用解析限定符。
	scanned map[string]string
	eps     []*EndpointIR
	errs    []string
}

// scanQualifiers 汇总被扫描包的包名 → import 路径。同名多包视为歧义（""），
// 不作为 oapi:middleware 限定符解析来源。
func scanQualifiers(packages []*Package) map[string]string {
	m := map[string]string{}
	for _, p := range packages {
		if prev, dup := m[p.Name]; dup {
			if prev != p.ImportPath {
				m[p.Name] = ""
			}
			continue
		}
		m[p.Name] = p.ImportPath
	}
	return m
}

func (b *irBuilder) errf(format string, args ...any) {
	b.errs = append(b.errs, fmt.Sprintf(format, args...))
}

// verifyMWRef 中间件/拦截器源码引用校验：引用目标在扫描包内时核对包级
// 函数存在；拦截器引用另做轻量签名校验（深层类型一致性交给编译器）。
func (b *irBuilder) verifyMWRef(byImport map[string]*Package, ep *EndpointIR, ref MWRef, kind string) {
	p, ok := byImport[ref.Import]
	if !ok {
		return // 非扫描包（自身 import / 完整路径解析）：交给编译器
	}
	for _, md := range p.methods[PKGFlag+p.Name] {
		if md.Name.Name == ref.Name {
			b.verifyICSignature(ep, kind, md, ref)
			return
		}
	}
	b.errf("%s.%s：%s %s.%s 在包 %s 中不存在（包级函数）", ep.Owner, ep.Handler, kind, ref.Qualifier, ref.Name, ref.Import)
}

// verifyICSignature 拦截器引用的轻量签名校验：hinge.Interceptor 为
// 5 参 1 返（ctx/ep/reader/sink/next → error）。拦的是把框架原生中间件
// 误标成 oapi:interceptor 的手误；深层类型一致性交给编译器。
func (b *irBuilder) verifyICSignature(ep *EndpointIR, kind string, md *ast.FuncDecl, ref MWRef) {
	if kind != "oapi:interceptor" || md.Type == nil {
		return
	}
	if md.Type.Params.NumFields() != 5 || md.Type.Results.NumFields() != 1 {
		b.errf("%s.%s：oapi:interceptor %s.%s 签名不符（hinge.Interceptor 为 5 参 1 返；框架原生中间件请用 oapi:middleware）", ep.Owner, ep.Handler, ref.Qualifier, ref.Name)
	}
}

// buildIR 从扫描包构建全部端点 IR（含完整校验）。
func buildIR(packages []*Package, entryPoints []EntryPointConfig) ([]*EndpointIR, error) {
	b := &irBuilder{packages: packages, scanned: scanQualifiers(packages)}
	for _, pkg := range packages {
		b.buildPackage(pkg)
	}
	// 跨包同名 Enterpoint 检测：生成的注册函数/表按结构体名命名，重名会互相冲突
	ownerPkg := map[string]string{}
	for _, ep := range b.eps {
		if prev, dup := ownerPkg[ep.Owner]; dup && prev != ep.Pkg.ImportPath {
			b.errf("Enterpoint %s 跨包重名（%s 与 %s）：生成的注册函数与表会冲突，请重命名结构体", ep.Owner, prev, ep.Pkg.ImportPath)
			continue
		}
		ownerPkg[ep.Owner] = ep.Pkg.ImportPath
	}
	// oapi:parent 挂载链展平（子声明式）：挂载关系由每个 Enterpoint 的 struct 级
	// oapi:parent 注解声明（父 Enterpoint 结构体名，纯名），沿祖先链（先根后叶）合成——
	//   FullPath   = 祖先链前缀（各节点 oapi:prefix 串联）+ 自身 FullPath；
	//   MountMWs   = 祖先链各节点 struct 级 oapi:middleware（根→叶）；
	//   MountICs   = 祖先链各节点 struct 级 oapi:interceptor（根→叶）。
	// 运行时仍是同一张平铺端点表，无嵌套结构。Config 补充不沿链（per-ep）。
	{
		mountEps := map[string][]*EndpointIR{}
		for _, ep := range b.eps {
			mountEps[ep.Owner] = append(mountEps[ep.Owner], ep)
		}
		type mountBase struct {
			prefix string  // 子树挂载前缀（祖先链 oapi:prefix 串联 + 自身）
			mws    []MWRef // 祖先链（含自身）struct 级 oapi:middleware
			ics    []MWRef // 祖先链（含自身）struct 级 oapi:interceptor
		}
		broken := map[string]bool{}
		// 预检：悬空 / 自引用 / PKG 引用 / 成环（沿链行走，环上全链标记 broken）
		mountOwners := make([]string, 0, len(mountEps))
		for owner := range mountEps {
			mountOwners = append(mountOwners, owner)
		}
		sort.Strings(mountOwners)
		for _, owner := range mountOwners {
			self := mountEps[owner][0]
			if self.Parent == "" || broken[owner] {
				continue
			}
			switch {
			case self.Parent == owner:
				b.errf("Enterpoint %s：oapi:parent 不能指向自身", owner)
				broken[owner] = true
			case strings.HasPrefix(self.Parent, PKGFlag):
				b.errf("Enterpoint %s：oapi:parent %q 指向包级函数端点（不允许，父必须是 Enterpoint 结构体）", owner, self.Parent)
				broken[owner] = true
			}
			if _, exists := mountEps[self.Parent]; !exists && !broken[owner] {
				b.errf("Enterpoint %s：oapi:parent %q 未命中任何扫描到的 Enterpoint（检查拼写，或父结构体是否含 oapi:route 端点）", owner, self.Parent)
				broken[owner] = true
			}
		}
		for _, owner := range mountOwners {
			if broken[owner] {
				continue
			}
			seen := map[string]bool{}
			cur := owner
			for {
				if seen[cur] {
					chain := make([]string, 0, len(seen))
					for o := range seen {
						chain = append(chain, o)
					}
					sort.Strings(chain)
					b.errf("Enterpoint %s：oapi:parent 链成环（涉及 %s），请检查 oapi:parent 声明", owner, strings.Join(chain, " → "))
					for o := range seen {
						broken[o] = true
					}
					break
				}
				seen[cur] = true
				next := mountEps[cur][0].Parent
				if next == "" {
					break
				}
				cur = next
			}
		}
		// 子树基座计算（memoized；broken 节点返回空基座，后代沿 broken 传播跳过应用）
		bases := map[string]*mountBase{}
		var baseOf func(owner string) *mountBase
		baseOf = func(owner string) *mountBase {
			if b, ok := bases[owner]; ok {
				return b
			}
			self := mountEps[owner][0]
			var ctx *mountBase
			if self.Parent == "" {
				ctx = &mountBase{
					prefix: self.Prefix,
					mws:    append([]MWRef{}, self.GroupMWs...),
					ics:    append([]MWRef{}, self.GroupICs...),
				}
			} else {
				p := baseOf(self.Parent)
				ctx = &mountBase{
					prefix: joinRoutePath(p.prefix, self.Prefix),
					mws:    append(append([]MWRef{}, p.mws...), self.GroupMWs...),
					ics:    append(append([]MWRef{}, p.ics...), self.GroupICs...),
				}
			}
			bases[owner] = ctx
			return ctx
		}
		for _, owner := range mountOwners {
			eps := mountEps[owner]
			self := eps[0]
			if self.Parent == "" || broken[owner] || broken[self.Parent] {
				continue // 根挂载无展平动作；诊断节点跳过应用
			}
			p := baseOf(self.Parent) // 父的子树基座（祖先链 + 父自身）
			for _, ep := range eps {
				// 防呆：自身路径已含挂载前缀 = 注解写了全路径的常见笔误
				if p.prefix != "" && (ep.FullPath == p.prefix || strings.HasPrefix(ep.FullPath, p.prefix+"/")) {
					b.errf("%s.%s：路径 %q 已含挂载前缀 %q——oapi:prefix / oapi:route 相对挂载点声明，请去掉重叠段", ep.Owner, ep.Handler, ep.FullPath, p.prefix)
					continue
				}
				ep.FullPath = joinRoutePath(p.prefix, ep.FullPath)
				ep.MountMWs = append([]MWRef{}, p.mws...)
				ep.MountICs = append([]MWRef{}, p.ics...)
			}
			if len(p.mws)+len(p.ics) > 0 {
				fmt.Fprintf(os.Stderr, "hinge gen: 挂载 %s ← %s：前缀 %q，沿链继承中间件 %d / 拦截器 %d\n",
					owner, self.Parent, p.prefix, len(p.mws), len(p.ics))
			}
		}
	}
	// EntryPointConfig 程序化补充（per-ep，不沿挂载链）：
	//   Middlewares → 框架原生中间件，反射取名后发射为组级源码引用；
	//   Interceptors → 内核拦截器，反射取名后发射为 extra 源码引用。
	// 两条通道不得混排。gen.Run 与 generate.go 同进程，运行时值反射取名后
	// 发射为源码引用。
	if len(entryPoints) > 0 {
		byOwner := map[string]EntryPointConfig{}
		for _, ec := range entryPoints {
			byOwner[string(ec.Name)] = ec
		}
		// FuncDecls 消费跟踪：未命中任何端点的键 = 拼写/重构失配，覆写会静默失效，
		// 块尾统一警告（P0-2）。
		usedFuncDecls := map[FuncId]bool{}
		configured := map[string]bool{}
		for _, ep := range b.eps {
			ec, ok := byOwner[ep.Owner]
			if !ok {
				continue
			}
			configured[ep.Owner] = true
			if rm, ok := ec.FuncDecls[FuncId(funcIdOf(ep))]; ok {
				usedFuncDecls[FuncId(funcIdOf(ep))] = true
				if changed := applyRouteMeta(ep, rm); len(changed) > 0 {
					fmt.Fprintf(os.Stderr, "hinge gen: 注：%s.%s 被 EntryPointConfig.FuncDecls 覆写：%s\n",
						ep.Owner, ep.Handler, strings.Join(changed, ", "))
				}
			}
			for i, mw := range ec.Middlewares {
				ref, imp, isIC, err := middlewareRef(mw)
				if err != nil {
					b.errf("Enterpoint %s Middlewares[%d]: %v", ep.Owner, i, err)
					continue
				}
				if ref == "nil" {
					continue // nil 值不发射
				}
				if isIC {
					b.errf("Enterpoint %s Middlewares[%d]: hinge.Interceptor 请放 EntryPointConfig.Interceptors（两条通道不得混排：Middlewares = 框架原生 → 组级直挂，Interceptors = 内核拦截器 → extra）", ep.Owner, i)
					continue
				}
				ep.RouteMWs = append(ep.RouteMWs, RouteMWRef{Ref: ref, Import: imp})
				if imp != "" {
					ep.RouteMWImports = append(ep.RouteMWImports, imp)
				}
			}
			for i, ic := range ec.Interceptors {
				ref, imp, isIC, err := middlewareRef(ic)
				if err != nil {
					b.errf("Enterpoint %s Interceptors[%d]: %v", ep.Owner, i, err)
					continue
				}
				if ref == "nil" {
					continue // nil 值不发射
				}
				if !isIC {
					b.errf("Enterpoint %s Interceptors[%d]: 值不是 hinge.Interceptor（框架原生中间件请放 Middlewares）", ep.Owner, i)
					continue
				}
				dot := strings.LastIndex(ref, ".")
				ep.ConfigICs = append(ep.ConfigICs, MWRef{Qualifier: ref[:dot], Name: ref[dot+1:], Import: imp})
			}
		}
		// 未命中警告：Config Name 拼错（补充静默失效）与 FuncDecls 键失配都必须可见
		for _, ec := range entryPoints {
			name := string(ec.Name)
			if !configured[name] {
				fmt.Fprintf(os.Stderr, "hinge gen: 警告：EntryPointConfig %q 未命中任何扫描到的 Enterpoint（检查 Name 拼写），配置未生效\n", name)
			}
			for key := range ec.FuncDecls {
				if !usedFuncDecls[key] {
					fmt.Fprintf(os.Stderr, "hinge gen: 警告：FuncDecls 键 %q 未命中任何端点（函数改名/移动后可能失配），覆写未生效\n", key)
				}
			}
		}
	}
	// 路由级/组级引用的存在性校验：引用目标在扫描包内时核对包级函数名
	//（签名兼容性交给编译器；这里只防手误拼错函数名）。
	byImport := map[string]*Package{}
	for _, p := range packages {
		byImport[p.ImportPath] = p
	}
	for _, ep := range b.eps {
		for _, ref := range ep.AnnoMWs {
			b.verifyMWRef(byImport, ep, ref, "oapi:middleware")
		}
		for _, ref := range ep.GroupMWs {
			b.verifyMWRef(byImport, ep, ref, "oapi:middleware")
		}
		for _, ref := range ep.AnnoICs {
			b.verifyMWRef(byImport, ep, ref, "oapi:interceptor")
		}
		for _, ref := range ep.GroupICs {
			b.verifyMWRef(byImport, ep, ref, "oapi:interceptor")
		}
		for _, ref := range ep.MountICs {
			b.verifyMWRef(byImport, ep, ref, "oapi:interceptor")
		}
		for _, ref := range ep.ConfigICs {
			b.verifyMWRef(byImport, ep, ref, "oapi:interceptor")
		}
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

// methodEntry 有效方法集条目：md 为方法声明，declOwner 为声明所属类型名
// （自身方法 = 查询类型；提升方法 = 嵌入类型名）。
type methodEntry struct {
	md        *ast.FuncDecl
	declOwner string
}

// effectiveMethods 计算类型的有效方法集（与 Go 方法提升语义对齐）：
// 自身方法 ∪ 匿名嵌入（递归）的同包导出结构体方法。声明序：自身在前、
// 嵌入按字段序递归；同一声明类型只纳入一次（菱形嵌入去重）。
// 跨包 / 接口 / 非结构体嵌入不参与（v1 同包约束，静默跳过）。
func effectiveMethods(pkg *Package, owner string) []methodEntry {
	seen := map[string]bool{}
	var out []methodEntry
	var walk func(t string)
	walk = func(t string) {
		if seen[t] {
			return
		}
		seen[t] = true
		for _, md := range pkg.methods[t] {
			out = append(out, methodEntry{md: md, declOwner: t})
		}
		if si, ok := pkg.structOf(t); ok {
			for _, sf := range si.st.Fields.List {
				if len(sf.Names) != 0 {
					continue
				}
				et := sf.Type
				if se, ok := et.(*ast.StarExpr); ok {
					et = se.X
				}
				id, ok := et.(*ast.Ident)
				if !ok || !ast.IsExported(id.Name) || id.Name == t {
					continue
				}
				if _, isStruct := pkg.structOf(id.Name); isStruct {
					walk(id.Name)
				}
			}
		}
	}
	walk(owner)
	return out
}

func (b *irBuilder) buildPackage(pkg *Package) {
	// 找出全部 Enterpoint：拥有 oapi:route 有效方法的接收者结构体
	//（直接方法 + 端点集提升：嵌入 Enterpoint 的结构体继承其端点，亦为 owner）。
	owners := map[string]bool{}
	var direct []string
	for recv, mds := range pkg.methods {
		for _, md := range mds {
			kv, _ := annotations(md.Doc)
			for _, pair := range kv {
				if pair[0] == "route" {
					if !owners[recv] {
						owners[recv] = true
						direct = append(direct, recv)
					}
					break
				}
			}
		}
	}
	// 端点集提升传播：嵌入 Enterpoint 类型（直接/递归）的结构体同为 owner。
	// 反向嵌入索引：被嵌入类型 → 嵌入方列表。
	consumers := map[string][]string{}
	for _, f := range pkg.Files {
		for name, si := range f.structs {
			for _, sf := range si.st.Fields.List {
				if len(sf.Names) != 0 {
					continue
				}
				et := sf.Type
				if se, ok := et.(*ast.StarExpr); ok {
					et = se.X
				}
				if id, ok := et.(*ast.Ident); ok && id.Name != name {
					consumers[id.Name] = append(consumers[id.Name], name)
				}
			}
		}
	}
	queue := append([]string{}, direct...)
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		for _, c := range consumers[t] {
			if !owners[c] {
				owners[c] = true
				queue = append(queue, c)
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
			case "parent":
				if value == "" {
					b.errf("Enterpoint %s：oapi:parent 值不能为空（父 Enterpoint 结构体名）", owner)
					return
				}
				if value == owner {
					b.errf("Enterpoint %s：oapi:parent 不能指向自身", owner)
					return
				}
				sa.Parent = value
			case "tag":
				if value != "" {
					sa.Tags = append(sa.Tags, value)
				}
			case "auth", "limit":
				b.errf("Enterpoint %s：注解 oapi:%s 已移除（v0.2 二分：框架原生中间件用 oapi:middleware，内核拦截器用 oapi:interceptor，值均为函数引用）", owner, key)
				return
			case "timeout":
				sa.TimeoutStr = value
			case "middleware":
				if value != "" {
					sa.MWs = append(sa.MWs, value)
				}
			case "interceptor":
				if value != "" {
					sa.ICs = append(sa.ICs, value)
				}
			default:
				b.errf("Enterpoint %s：未知 struct 级注解 oapi:%s（允许 prefix/parent/tag/timeout/middleware/interceptor）", owner, key)
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
	// 有效方法集（与 Go 方法提升语义对齐）：自身方法 ∪ 嵌入 Enterpoint 的提升方法。
	// 提升端点：Owner = 本类型（spec/注册函数变体名，与被嵌入方天然不冲突），
	// 路径用本类型的 oapi:prefix，方法级注解随方法走，struct 级注解不随（两身份解耦）。
	methods := effectiveMethods(pkg, owner)
	direct := map[string]bool{}
	for _, me := range methods {
		if me.declOwner == owner {
			direct[me.md.Name.Name] = true
		}
	}
	shadowWarned := map[string]bool{}
	for _, me := range methods {
		md := me.md
		if me.declOwner != owner && direct[md.Name.Name] {
			// 自身方法遮蔽提升端点（Go 遮蔽语义合法）：提升端点不发射，必须可见
			if !shadowWarned[md.Name.Name] {
				shadowWarned[md.Name.Name] = true
				fmt.Fprintf(os.Stderr, "hinge gen: 警告：Enterpoint %s 的方法 %s 遮蔽了嵌入自 %s 的同名端点，后者不发射\n", owner, md.Name.Name, me.declOwner)
			}
			continue
		}
		// var routeMeta *RouteMeta
		// if len(md.Recv.List) != 0 {
		// 	md.Recv.List[0].Type
		// }
		kv, docLines := annotations(md.Doc)
		route := ""
		ma := map[string]string{}
		var mTags, mMWs, mICs []string
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
					mMWs = append(mMWs, value)
				}
			case "interceptor":
				if value != "" {
					mICs = append(mICs, value)
				}
			case "auth", "limit", "intercepter":
				b.errf("%s.%s：注解 oapi:%s 已移除（v0.2 二分：框架原生中间件用 oapi:middleware，内核拦截器用 oapi:interceptor，值均为函数引用）", owner, md.Name.Name, key)
			case "timeout":
				ma["timeout"] = value
			case "status":
				ma["status"] = value
			case "envelope":
				ma["envelope"] = value
			case "deprecated":
				deprecated = true
			default:
				b.errf("%s.%s：未知方法级注解 oapi:%s（允许 route/tag/timeout/status/deprecated/envelope/middleware/interceptor）", owner, md.Name.Name, key)
			}
		}
		if !hasRoute {
			continue
		}
		ep := &EndpointIR{
			Owner:      owner,
			Pkg:        pkg,
			Prefix:     sa.Prefix,
			Parent:     sa.Parent,
			Handler:    md.Name.Name,
			Summary:    firstNonEmpty(docLines...),
			Tags:       append(append([]string{}, sa.Tags...), mTags...),
			Deprecated: deprecated,
			Envelope:   ma["envelope"],
			TimeoutStr: firstNonEmpty(ma["timeout"], sa.TimeoutStr),
		}
		// 注解二分（严格解析，失败即报错）：
		//   oapi:middleware → 框架原生：结构体级 → 组级（scoped Group），方法级 → 路由级直挂；
		//   oapi:interceptor → 内核拦截器：结构体级 → owner 全端点 extra，方法级 → 本端点 extra。
		pos := owner + "." + md.Name.Name
		ep.GroupMWs = b.resolveAnnoRefs(pkg, b.scanned, sa.MWs, "oapi:middleware", owner+"（struct 级）")
		ep.GroupICs = b.resolveAnnoRefs(pkg, b.scanned, sa.ICs, "oapi:interceptor", owner+"（struct 级）")
		ep.AnnoMWs = b.resolveAnnoRefs(pkg, b.scanned, mMWs, "oapi:middleware", pos)
		ep.AnnoICs = b.resolveAnnoRefs(pkg, b.scanned, mICs, "oapi:interceptor", pos)
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
				ep.BodyKind = bodyKindOf(fs)
				if ep.BodyKind == "multipart" {
					for _, f := range fs.Fields {
						isFile := f.Class == classFile || f.Class == classFileSlice
						switch {
						case isFile && f.In != "form":
							b.errf("%s：multipart 文件字段 %s 必须声明 form 标签（否则文件静默丢失）", pos, f.GoName)
						case !isFile && f.In != "form":
							src := f.In
							if src == "" {
								src = "无标签（JSON 语义）"
							}
							b.errf("%s：multipart body 字段 %s 当前为 %s；multipart value 只认 form 标签，query 入参请拆到 Q，JSON 字段请勿与 multipart 混排", pos, f.GoName, src)
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
