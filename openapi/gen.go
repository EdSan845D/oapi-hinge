//go:build openapi

// Package openapi 开发期 OpenAPI 文档生成器：从统一路由注册表生成 OpenAPI 3.1 规范。
// 纯 kin-openapi 实现：类型反射生成 schema（schema.go）、
// 路由树递归生成 operation。仅 -tags openapi 构建，release 构建零开发期依赖。
//
// 用法：go run -tags openapi . -out openapi.yaml
package openapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/contract/response"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// respWrapperIface 响应定制壳接口（contract.Response[R]）
var respWrapperIface = reflect.TypeOf((*contract.ResponseWrapper)(nil)).Elem()

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

// 壳推导状态（dev 工具单线程；generate 重置）。
// envelopeInstance 由 OptionWithEnvelope 注入运行时实际壳实例；
// envelopeSchemaCustom 标记 OptionWithEnvelopeSchema 手写壳 schema（仅无法从壳实例推导时使用）。
var (
	envelopeSchema       EnvelopeSchema = defaultEnvelopeSchema
	envelopeSchemaCustom bool
	envelopeInstance     response.Envelope
)

// Option 文档生成选项：以函数式方式注入文档元信息
// （Info / Servers / SecuritySchemes 等，见 OptionWithXxx）。
type Option = func(*openapi3.T)

// Generate 生成 OpenAPI 文档并写入 out（.yaml/.yml -> YAML，.json -> JSON）。
// groups 为路由分组树（routes.All()）；opts 注入文档元信息。
// 警告（operationID 重复、schema 名升级、未匹配注册、缺 Summary）输出到 stderr；
// 需要把警告当错误（CI）用 GenerateStrict。
func Generate(out string, groups []*contract.Group, opts ...Option) error {
	_, err := generate(out, groups, false, opts...)
	return err
}

// GenerateStrict 与 Generate 相同，但存在警告时返回错误（文档规范检查进 CI）。
func GenerateStrict(out string, groups []*contract.Group, opts ...Option) error {
	_, err := generate(out, groups, true, opts...)
	return err
}

// buildDoc 生成文档对象（两轮：探测收集类型 → 统一命名 → 正式构建）。
// 警告返回给调用方（Generate 打 stderr；GenerateStrict 转 error）。
func buildDoc(groups []*contract.Group, opts ...Option) (*openapi3.T, []string, error) {
	resetEnvelopeState()
	resetUsage()

	// pass 1（探测）：只收集组件类型集合（含壳推导类型），产物丢弃
	probeDoc := newDoc()
	applyOpts(probeDoc, opts)
	probe := &specGen{doc: probeDoc, sb: newSchemaBuilder(probeDoc, nil), probe: true, opIDs: map[string]string{}, tagDescs: map[string]string{}}
	buildSpec(probe, groups)

	// 两阶段命名：裸名优先，跨包同名冲突整体升级
	names, warnings := assignNames(probe.sb.seen)
	if commentParser != nil && !sourceComments {
		warnings = append(warnings, "RegisterCommentParser: 已注册注释解析器但未开启 OptionWithSourceComments（本次生成不生效）")
	}
	if sourceComments {
		if mp, _ := findModule(); mp == "" {
			warnings = append(warnings, "source comments enabled but go.mod not found（无法定位主模块，注释解析已跳过）")
		}
	}

	// pass 2：正式生成
	doc := newDoc()
	applyOpts(doc, opts)
	g := &specGen{doc: doc, sb: newSchemaBuilder(doc, names), opIDs: map[string]string{}, tagDescs: map[string]string{}}
	buildSpec(g, groups)

	// 补录路由合并 + 未匹配注册检查
	mergeManualPaths(doc)
	warnings = append(warnings, g.warns...)
	warnings = append(warnings, unmatchedRegistrations()...)
	// 生成期规范校验（默认开启）：程序化构建的 doc 内部 $ref 尚未解析（Value 为 nil），
	// kin-openapi 的 Validate 不解析引用，因此先 YAML 序列化往返再校验——
	// 与最终交付内容完全一致，同时能抓住序列化产物的问题（如非法组件名字符）。
	if data, merr := yaml.Marshal(doc); merr == nil {
		loaded, lerr := openapi3.NewLoader().LoadFromData(data)
		if lerr != nil {
			warnings = append(warnings, "spec validation failed: "+lerr.Error())
		} else if verr := loaded.Validate(context.Background()); verr != nil {
			warnings = append(warnings, "spec validation failed: "+verr.Error())
		}
	}
	return doc, warnings, nil
}

// Build 生成文档对象（不落盘）：自建 /docs、推送网关等自定义消费场景。
// 警告输出 stderr；需要警告即失败用 GenerateStrict。
func Build(groups []*contract.Group, opts ...Option) (*openapi3.T, error) {
	doc, warnings, err := buildDoc(groups, opts...)
	if err != nil {
		return nil, err
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "openapi warning:", w)
	}
	return doc, nil
}

// generate 两轮生成：探测轮收集全部组件类型 → 统一命名 → 正式轮产出规范。
func generate(out string, groups []*contract.Group, strict bool, opts ...Option) ([]string, error) {
	doc, warnings, err := buildDoc(groups, opts...)
	if err != nil {
		return warnings, err
	}
	if strict && len(warnings) > 0 {
		return warnings, fmt.Errorf("openapi: %d 个警告:\n  - %s", len(warnings), strings.Join(warnings, "\n  - "))
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "openapi warning:", w)
	}

	var data []byte
	if strings.HasSuffix(strings.ToLower(out), ".json") {
		data, err = json.MarshalIndent(doc, "", "  ")
	} else {
		data, err = yaml.Marshal(doc)
	}
	if err != nil {
		return warnings, fmt.Errorf("marshal spec: %w", err)
	}
	return warnings, os.WriteFile(out, data, 0o644)
}

func newDoc() *openapi3.T {
	return &openapi3.T{
		OpenAPI: "3.1.0",
		Info:    &openapi3.Info{Title: "API", Version: "0.0.0"},
		Servers: openapi3.Servers{{URL: "/"}},
		Paths:   openapi3.NewPaths(),
		Components: &openapi3.Components{
			Schemas: openapi3.Schemas{},
		},
	}
}

func applyOpts(doc *openapi3.T, opts []Option) {
	for _, opt := range opts {
		opt(doc)
	}
}

func resetEnvelopeState() {
	envelopeSchema = defaultEnvelopeSchema
	envelopeSchemaCustom = false
	envelopeInstance = nil
	sourceComments = false
}

// specGen 一次生成（探测/正式）的上下文。
type specGen struct {
	doc      *openapi3.T
	sb       *schemaBuilder
	probe    bool              // 探测轮：只收集类型，不产出警告
	opIDs    map[string]string // operationID -> "METHOD path"（查重，正式轮）
	tagNames []string          // 顶层 tags 声明（首次出现顺序）
	tagDescs map[string]string
	warns    []string
}

// noteTag 记录顶层 tag（组 Description 作为 tag 描述，首个非空生效）。
func (g *specGen) noteTag(name, desc string) {
	if name == "" {
		return
	}
	if _, ok := g.tagDescs[name]; !ok {
		g.tagNames = append(g.tagNames, name)
		g.tagDescs[name] = desc
		return
	}
	if desc != "" && g.tagDescs[name] == "" {
		g.tagDescs[name] = desc
	}
}

func buildSpec(g *specGen, groups []*contract.Group) {
	checkDuplicates(groups)
	for _, gr := range groups {
		addGroup(g, gr, "", nil)
	}
	// 顶层 tags 声明（规范推荐；Swagger UI 按此分组/排序）
	if len(g.tagNames) > 0 {
		tags := openapi3.Tags{}
		for _, name := range g.tagNames {
			tags = append(tags, &openapi3.Tag{Name: name, Description: g.tagDescs[name]})
		}
		g.doc.Tags = tags
	}
}

// addGroup 递归生成一个分组的所有 operation。
// inherited 为父组继承下来的中间件；组 Tags 与路由 Tags 合并。
func addGroup(g *specGen, gr *contract.Group, prefix string, inherited []any) {
	path := prefix + gr.Prefix
	mws := append(append([]any{}, inherited...), gr.Middlewares...)
	for _, tg := range gr.Tags {
		g.noteTag(tg, gr.Description)
	}
	for _, r := range gr.Routes {
		addOperation(g, r, path, gr.Tags, mws)
	}
	for _, child := range gr.Children {
		addGroup(g, child, path, mws)
	}
}

// addOperation 为一个路由生成 OpenAPI 操作。
// Handler 模板：func(context.Context, Q, B) (R, error)
//   - Q：query 参数来自 `query` 标签，path 参数来自路径 {id}
//   - B：interface{} 表示无 body，否则整包作为 application/json 请求体
//   - R：Data 段 schema；*FileStream 输出二进制流，接口类型（Empty/any）输出任意 JSON 值 schema
//   - 中间件文档钩子按函数名匹配（RegisterMiddlewareDoc）先应用，
//     DescribeRoute 的 Hook 最后应用（可覆盖以上所有）
func addOperation(g *specGen, r contract.Route, groupPath string, groupTags []string, mws []any) {
	if err := contract.CheckHandler(r.Handler); err != nil {
		// 生成期签名校验：给出路由定位，避免 In(1) 等下标访问 panic 信息晦涩
		panic(fmt.Sprintf("openapi: %s %s: invalid handler: %v", r.Method, r.Path, err))
	}

	// 路由级纯文档增强（Hide 在探测/正式两轮都直接跳过，类型集合保持一致）
	rd, hasDoc := routeDocFor(r.Handler)
	if hasDoc && rd.Hide {
		return
	}

	op := openapi3.NewOperation()
	// Responses 先初始化：中间件文档钩子可能立即写入响应（如 401）
	op.Responses = openapi3.NewResponses()
	op.Summary = r.Summary
	op.Description = r.Description
	op.OperationID = operationID(r.Handler)
	if hasDoc && rd.OperationID != "" {
		op.OperationID = rd.OperationID
	}
	op.Deprecated = r.Deprecated
	op.Tags = mergeTags(groupTags, r.Tags)
	for _, tg := range op.Tags {
		g.noteTag(tg, "")
	}
	applyHooks(op, mws)

	// handler 源码注释兜底 Summary/Description（RouteMeta 未写时；首行 → Summary，其余 → Description）
	if r.Summary == "" && sourceComments {
		if hc := handlerCommentOf(r.Handler); hc != "" {
			lines := strings.SplitN(hc, "\n", 2)
			op.Summary = strings.TrimSpace(lines[0])
			if len(lines) == 2 && op.Description == "" {
				op.Description = strings.TrimSpace(lines[1])
			}
		}
	}

	// operationID 查重 + Summary 缺失提示（正式轮）
	routeKey := r.Method + " " + groupPath + r.Path
	if !g.probe {
		if prev, dup := g.opIDs[op.OperationID]; dup {
			g.warns = append(g.warns, fmt.Sprintf("duplicate operationID %q: %s vs %s（用 DescribeRoute OperationID 区分）",
				op.OperationID, prev, routeKey))
		} else {
			g.opIDs[op.OperationID] = routeKey
		}
		if op.Summary == "" {
			g.warns = append(g.warns, "missing summary: "+routeKey)
		}
	}

	fn := reflect.TypeOf(r.Handler)
	qT, bT, rT := fn.In(1), fn.In(2), fn.Out(0)

	// path 参数：优先取 Q 中 path 标签字段的类型与描述（缺省回退 string）
	pathFields := pathFieldsOf(qT)
	for _, name := range pathParams(r.Path) {
		p := openapi3.NewPathParameter(name)
		if f, ok := pathFields[name]; ok {
			p.Schema = paramSchema(f.Type)
			if !contract.HasParamBinder(f.Type) {
				p.Schema = applyFieldTags(p.Schema, f)
				if f.Type.Kind() == reflect.Pointer && p.Schema.Value != nil {
					p.Schema.Value.Nullable = true
				}
			}
			if d, ok := f.Tag.Lookup("description"); ok && d != "" {
				p.Description = d
			}
			// 字段注释 → 参数描述（description 标签优先）
			p.Schema = applyFieldComment(p.Schema, qT, f.Name)
			if p.Description == "" && p.Schema.Value != nil {
				p.Description = p.Schema.Value.Description
			}
		} else {
			p.Schema = openapi3.NewStringSchema().NewRef()
		}
		op.AddParameter(p)
	}
	for _, p := range queryParams(qT) {
		op.AddParameter(p)
	}

	if bT.Kind() != reflect.Interface {
		ref := g.sb.ref(bT)
		op.RequestBody = &openapi3.RequestBodyRef{Value: openapi3.NewRequestBody().
			WithRequired(true).
			WithDescription("Request body for " + bT.String()).
			WithContent(openapi3.NewContentWithSchemaRef(ref, []string{"application/json"}))}
	}

	// 成功响应码：路由级默认状态码 > 200（配合 contract.RouteMeta.DefaultStatusCode）
	successCode := strconv.Itoa(r.DefaultStatusCode)
	if r.DefaultStatusCode == 0 {
		successCode = "200"
	}
	// 401 等鉴权响应由中间件文档钩子按需声明（RegisterMiddlewareDoc），
	// 不对全部接口硬编码 401（公开接口如 /health 不应出现 401）

	if rT.Kind() == reflect.Pointer {
		rT = rT.Elem()
	}
	// Response[R] 包装 → 取 Data 字段类型作为响应 schema
	if rT.Implements(respWrapperIface) {
		if f, ok := rT.FieldByName("Data"); ok {
			rT = f.Type
		}
	}

	var successResp *openapi3.ResponseRef
	switch {
	case rT == reflect.TypeOf(contract.FileStream{}):
		bin := openapi3.NewStringSchema()
		bin.Format = "binary"
		successResp = &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription("文件二进制流").
			WithContent(openapi3.NewContentWithSchemaRef(&openapi3.SchemaRef{Value: bin}, []string{"application/octet-stream"}))}
		op.Responses.Set("404", &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription("文件不存在")})
	case rT.Kind() == reflect.Interface:
		// 接口类型响应（Empty=any / 裸 any）：空 schema 表示任意 JSON 值，
		// 实际返回 null 时序列化为 data: null
		successResp = okResponse(g, r, &openapi3.SchemaRef{Value: openapi3.NewSchema()})
	default:
		successResp = okResponse(g, r, g.sb.ref(rT))
	}
	applySuccessHeaders(successResp, rd.ResponseHeaders)
	op.Responses.Set(successCode, successResp)

	// 错误响应声明（DescribeRoute.Errors）
	if hasDoc {
		for _, ed := range rd.Errors {
			op.Responses.Set(strconv.Itoa(ed.Status), errorResponse(g, r, ed))
		}
	}

	// 路由文档钩子：中间件钩子之后、最后应用（兜底改写）
	if hasDoc && rd.Hook != nil {
		rd.Hook(op)
	}

	// 合并到 Paths：同路径不同 method 共存
	p := g.doc.Paths.Value(groupPath + r.Path)
	if p == nil {
		p = &openapi3.PathItem{}
		g.doc.Paths.Set(groupPath+r.Path, p)
	}
	p.SetOperation(r.Method, op)
}

// checkDuplicates 校验路由树中 path+method 无重复（文档期早期报错，避免静默覆盖）
func checkDuplicates(groups []*contract.Group) {
	seen := map[string]string{} // "GET /users/{id}" -> handler 名
	var walk func(gs []*contract.Group, prefix string)
	walk = func(gs []*contract.Group, prefix string) {
		for _, g := range gs {
			p := prefix + g.Prefix
			for _, r := range g.Routes {
				key := r.Method + " " + p + r.Path
				if prev, dup := seen[key]; dup {
					panic(fmt.Sprintf("duplicate route %s: %s vs %s", key, prev, operationID(r.Handler)))
				}
				seen[key] = operationID(r.Handler)
			}
			walk(g.Children, p)
		}
	}
	walk(groups, "")
}

// mergeTags 合并组 Tags 与路由 Tags（去重保序）
func mergeTags(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	seen := map[string]bool{}
	for _, t := range append(append([]string{}, a...), b...) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// effectiveEnvelope 返回路由实际生效的壳实例；nil 表示走手写壳 schema
// （OptionWithEnvelopeSchema 且未提供壳实例/路由级壳）。
func effectiveEnvelope(r contract.Route) response.Envelope {
	if r.Envelope != nil {
		return r.Envelope
	}
	if envelopeInstance != nil {
		return envelopeInstance
	}
	if envelopeSchemaCustom {
		return nil
	}
	return response.DefaultEnvelope{}
}

// okResponse 成功响应：壳形态由实际生效的壳实例推导（文档与运行时同构）。
func okResponse(g *specGen, r contract.Route, data *openapi3.SchemaRef) *openapi3.ResponseRef {
	env := effectiveEnvelope(r)
	var schema *openapi3.SchemaRef
	if env == nil {
		// 手写壳 schema：OptionWithEnvelopeSchema 且未提供壳实例/路由级壳
		schema = envelopeSchema(data)
	} else {
		status := r.DefaultStatusCode
		if status == 0 {
			status = http.StatusOK
		}
		// data 传 nil：壳形态与值无关，仅取结构
		schema = deriveEnvelopeSchema(g.sb, env.Success(status, nil), data)
	}
	return &openapi3.ResponseRef{Value: openapi3.NewResponse().
		WithDescription("OK").
		WithContent(openapi3.NewContentWithSchemaRef(schema, []string{"application/json"}))}
}

// errorResponse 生成错误响应：默认形态由路由实际生效的壳推导
// （含 code 默认值 / msg 示例注入，与运行时 resolveError 约定一致）；
// ErrorDecl.Schema 显式覆盖。
func errorResponse(g *specGen, r contract.Route, ed ErrorDecl) *openapi3.ResponseRef {
	desc := ed.Description
	if desc == "" {
		desc = fmt.Sprintf("HTTP %d", ed.Status)
	}
	var schemaRef *openapi3.SchemaRef
	if ed.Schema != nil {
		schemaRef = ed.Schema
	} else {
		code := ed.Code
		if code == 0 {
			if ed.Status == http.StatusOK {
				code = response.CodeError
			} else {
				code = ed.Status
			}
		}
		env := effectiveEnvelope(r)
		if env == nil {
			schemaRef = envelopeSchema(&openapi3.SchemaRef{Value: openapi3.NewSchema()})
		} else {
			wrapped := env.Failure(ed.Status, code, desc)
			schemaRef = deriveEnvelopeSchema(g.sb, wrapped, &openapi3.SchemaRef{Value: openapi3.NewSchema()})
			// 推导 schema 上注入具体 code 默认值与 msg 示例
			if s := schemaRef.Value; s != nil && s.Properties != nil {
				if cr := s.Properties["code"]; cr != nil && cr.Value != nil {
					cr.Value.Default = code
				}
				if mr := s.Properties["msg"]; mr != nil && mr.Value != nil {
					mr.Value.Example = desc
				}
			}
		}
	}
	return &openapi3.ResponseRef{Value: openapi3.NewResponse().
		WithDescription(desc).
		WithContent(openapi3.NewContentWithSchemaRef(schemaRef, []string{"application/json"}))}
}

// deriveEnvelopeSchema 调用壳实例的 Success/Failure 取返回值，反射生成壳 schema：
//   - 返回接口/nil → 透传壳：直接使用 data（如 RawEnvelope.Success）
//   - struct → 逐字段构建：interface 类型字段为业务数据位，用 data 替换；其余反射；壳本身内联
//   - map → additionalProperties 同上（如 RawEnvelope.Failure 的 map[string]any）
func deriveEnvelopeSchema(sb *schemaBuilder, wrapped any, data *openapi3.SchemaRef) *openapi3.SchemaRef {
	rv := reflect.ValueOf(wrapped)
	if !rv.IsValid() {
		return data
	}
	t := rv.Type()
	if t.Kind() == reflect.Interface {
		return data
	}
	switch t.Kind() {
	case reflect.Struct:
		s := openapi3.NewObjectSchema()
		s.Properties = openapi3.Schemas{}
		var required []string
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() || f.Anonymous {
				continue
			}
			name, omit := jsonName(f)
			if name == "" || name == "-" {
				continue
			}
			s.Properties[name] = envelopeFieldRef(sb, f.Type, data)
			if !omit {
				required = append(required, name)
			}
		}
		if len(required) > 0 {
			s.Required = required
		}
		return &openapi3.SchemaRef{Value: s}
	case reflect.Map:
		obj := openapi3.NewObjectSchema()
		obj.AdditionalProperties.Schema = envelopeFieldRef(sb, t.Elem(), data)
		return &openapi3.SchemaRef{Value: obj}
	default:
		return envelopeFieldRef(sb, t, data)
	}
}

// envelopeFieldRef 壳字段引用：interface 类型 = 业务数据位（data）；
// 其余类型正常反射（可能注册组件）。
func envelopeFieldRef(sb *schemaBuilder, t reflect.Type, data *openapi3.SchemaRef) *openapi3.SchemaRef {
	if t.Kind() == reflect.Interface {
		return data
	}
	return sb.ref(t)
}

// applySuccessHeaders 把声明的响应头写入成功响应。
func applySuccessHeaders(resp *openapi3.ResponseRef, decls []HeaderDecl) {
	if len(decls) == 0 || resp == nil || resp.Value == nil {
		return
	}
	if resp.Value.Headers == nil {
		resp.Value.Headers = openapi3.Headers{}
	}
	for _, hd := range decls {
		if hd.Name == "" {
			continue
		}
		h := &openapi3.Header{}
		h.Description = hd.Description
		h.Required = hd.Required
		if hd.Schema != nil {
			h.Schema = &openapi3.SchemaRef{Value: hd.Schema}
		} else {
			h.Schema = &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}
		}
		resp.Value.Headers[hd.Name] = &openapi3.HeaderRef{Value: h}
	}
}

// paramSchema 参数 schema：注册过 ParamBinder 文档 schema 的类型用注册值；
// 注册过绑定器但未声明文档 schema 的回退 string（HTTP 形态是原始串）；
// 普通类型按 kind 反射。
func paramSchema(t reflect.Type) *openapi3.SchemaRef {
	if s := binderSchemaFor(t); s != nil {
		return &openapi3.SchemaRef{Value: s}
	}
	if contract.HasParamBinder(t) {
		return &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}
	}
	return &openapi3.SchemaRef{Value: schemaByKind(t)}
}

// queryParams 从 Q 结构体提取 query 参数（query/form 标签，内嵌结构体递归展平）
func queryParams(t reflect.Type) []*openapi3.Parameter {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []*openapi3.Parameter
	var walk func(reflect.Type)
	walk = func(tt reflect.Type) {
		for i := 0; i < tt.NumField(); i++ {
			f := tt.Field(i)
			if f.Anonymous {
				ft := f.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft)
				}
				continue
			}
			if !f.IsExported() {
				continue
			}
			// header 标签优先（独立于 query/form）
			if hname, hok := tagValueOf(f, "header"); hok {
				p := openapi3.NewHeaderParameter(hname)
				if d, ok := f.Tag.Lookup("description"); ok && d != "" {
					p.Description = d
				}
				p.Schema = paramSchema(f.Type)
				if isRequiredOpenAPI(f) {
					p.Required = true
				}
				out = append(out, p)
				continue
			}
			name, ok := tagValueOf(f, "query")
			if !ok {
				name, ok = tagValueOf(f, "form")
			}
			if !ok {
				continue
			}
			p := openapi3.NewQueryParameter(name)
			if d, ok := f.Tag.Lookup("description"); ok && d != "" {
				p.Description = d
			}
			p.Schema = paramSchema(f.Type)
			if !contract.HasParamBinder(f.Type) {
				p.Schema = applyFieldTags(p.Schema, f)
				if f.Type.Kind() == reflect.Pointer && p.Schema.Value != nil {
					p.Schema.Value.Nullable = true
				}
			}
			if dv, ok := f.Tag.Lookup("default"); ok && dv != "" {
				p.Schema.Value.Default = parseDefault(dv, derefKind(f.Type))
			}
			if isRequiredOpenAPI(f) {
				p.Required = true
			}
			// 字段注释 → 参数描述（description 标签优先）
			p.Schema = applyFieldComment(p.Schema, tt, f.Name)
			if p.Description == "" && p.Schema.Value != nil {
				p.Description = p.Schema.Value.Description
			}
			out = append(out, p)
		}
	}
	walk(t)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func tagValueOf(f reflect.StructField, key string) (string, bool) {
	v := f.Tag.Get(key)
	if v == "" {
		return "", false
	}
	return strings.Split(v, ",")[0], true
}

// pathFieldsOf 提取 Q 中 path 标签字段（内嵌结构体递归），键为路径参数名
func pathFieldsOf(t reflect.Type) map[string]reflect.StructField {
	out := map[string]reflect.StructField{}
	if t == nil {
		return out
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return out
	}
	var walk func(reflect.Type)
	walk = func(tt reflect.Type) {
		for i := 0; i < tt.NumField(); i++ {
			f := tt.Field(i)
			if f.Anonymous {
				ft := f.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft)
				}
				continue
			}
			if !f.IsExported() {
				continue
			}
			if name, ok := tagValueOf(f, "path"); ok && name != "" {
				out[name] = f
			}
		}
	}
	walk(t)
	return out
}

func schemaByKind(t reflect.Type) *openapi3.Schema {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return openapi3.NewStringSchema()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return openapi3.NewIntegerSchema()
	case reflect.Float32, reflect.Float64:
		return openapi3.NewFloat64Schema()
	case reflect.Bool:
		return openapi3.NewBoolSchema()
	case reflect.Slice, reflect.Array:
		arr := openapi3.NewArraySchema()
		arr.Items = &openapi3.SchemaRef{Value: schemaByKind(t.Elem())}
		return arr
	default:
		return openapi3.NewStringSchema()
	}
}

// parseDefault default 标签值按字段类型转型（指针字段解引用；转型失败保留字符串）。
// 覆盖全标量类型：整数（64 位解析）、无符号、浮点、布尔；其余类型原样字符串。
func parseDefault(s string, kind reflect.Kind) any {
	switch kind {
	case reflect.String:
		return s
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v, err := strconv.ParseUint(s, 10, 64); err == nil {
			return v
		}
	case reflect.Float32, reflect.Float64:
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v
		}
	case reflect.Bool:
		if v, err := strconv.ParseBool(s); err == nil {
			return v
		}
	}
	return s
}

func pathParams(path string) []string {
	matches := pathParamRe.FindAllStringSubmatch(path, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// operationID 取 Handler 裸函数名（如 handlers.ListUsers -> ListUsers）；
// 跨包同名由 DescribeRoute.OperationID 覆盖 + 生成警告兜底
func operationID(h any) string {
	v := reflect.ValueOf(h)
	name := runtime.FuncForPC(v.Pointer()).Name()
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(name, "-fm")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}
