//go:build openapi

// Package openapi 开发期 OpenAPI 文档生成器：从端点表（[]hinge.Endpoint）生成 OpenAPI 3.1 规范。
// 纯 kin-openapi 实现：类型反射生成 schema（schema.go）、
// 端点表扁平遍历生成 operation。仅 -tags openapi 构建，release 构建零开发期依赖。
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
	"sort"
	"strconv"
	"strings"

	"github.com/EdSan845D/oapi-hinge/hinge"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// respWrapperIface 响应定制壳接口（hinge.Response[R]）
var respWrapperIface = reflect.TypeOf((*hinge.ResponseWrapper)(nil)).Elem()

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

// 壳推导状态（dev 工具单线程；generate 重置）。
// envelopeInstance 由 OptionWithEnvelope 注入运行时实际壳实例；
// envelopeSchemaCustom 标记 OptionWithEnvelopeSchema 手写壳 schema（仅无法从壳实例推导时使用）；
// envelopeSchemas 由 OptionWithEnvelopeSchemas 注册命名壳（hinge.Endpoint.Envelope 引用名）的文档 schema。
var (
	envelopeSchema       EnvelopeSchema = defaultEnvelopeSchema
	envelopeSchemaCustom bool
	envelopeInstance     hinge.Envelope
	envelopeSchemas      map[string]EnvelopeSchema
)

// Option 文档生成选项：以函数式方式注入文档元信息
// （Info / Servers / SecuritySchemes 等，见 OptionWithXxx）。
type Option = func(*openapi3.T)

// Generate 生成 OpenAPI 文档并写入 out（.yaml/.yml -> YAML，.json -> JSON）。
// eps 为端点表（hinge gen 产出的 Endpoints() 表或手写注册均可）；opts 注入文档元信息。
// 警告（operationID 重复、schema 名升级、未知命名壳、缺 Summary）输出到 stderr；
// 需要把警告当错误（CI）用 GenerateStrict。
func Generate(out string, eps []hinge.Endpoint, opts ...Option) error {
	_, err := generate(out, eps, false, opts...)
	return err
}

// GenerateStrict 与 Generate 相同，但存在警告时返回错误（文档规范检查进 CI）。
func GenerateStrict(out string, eps []hinge.Endpoint, opts ...Option) error {
	_, err := generate(out, eps, true, opts...)
	return err
}

// buildDoc 生成文档对象（两轮：探测收集类型 → 统一命名 → 正式构建）。
// 警告返回给调用方（Generate 打 stderr；GenerateStrict 转 error）。
func buildDoc(eps []hinge.Endpoint, opts ...Option) (*openapi3.T, []string, error) {
	resetEnvelopeState()

	// pass 1（探测）：只收集组件类型集合（含壳推导类型），产物丢弃
	probeDoc := newDoc()
	applyOpts(probeDoc, opts)
	probe := &specGen{doc: probeDoc, sb: newSchemaBuilder(probeDoc, nil), probe: true, opIDs: map[string]string{}, tagDescs: map[string]string{}}
	buildSpec(probe, eps)

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
	buildSpec(g, eps)

	// 补录路由合并
	mergeManualPaths(doc)
	warnings = append(warnings, g.warns...)
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
func Build(eps []hinge.Endpoint, opts ...Option) (*openapi3.T, error) {
	doc, warnings, err := buildDoc(eps, opts...)
	if err != nil {
		return nil, err
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "openapi warning:", w)
	}
	return doc, nil
}

// generate 两轮生成：探测轮收集全部组件类型 → 统一命名 → 正式轮产出规范。
func generate(out string, eps []hinge.Endpoint, strict bool, opts ...Option) ([]string, error) {
	doc, warnings, err := buildDoc(eps, opts...)
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
	envelopeSchemas = nil
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

// noteTag 记录顶层 tag（v0.2 端点表无分组描述来源，desc 传空；首个非空生效）。
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

// buildSpec 扁平遍历端点表，逐个生成 operation；最后写顶层 tags 声明。
func buildSpec(g *specGen, eps []hinge.Endpoint) {
	checkDuplicates(eps)
	for i := range eps {
		addOperation(g, &eps[i])
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

// checkDuplicates 校验端点表中 method+path 无重复（文档期早期报错，
// 避免 Paths.SetOperation 静默覆盖）
func checkDuplicates(eps []hinge.Endpoint) {
	seen := map[string]string{} // "GET /users/{id}" -> 端点名
	for i := range eps {
		ep := &eps[i]
		key := ep.Method + " " + ep.Path
		if prev, dup := seen[key]; dup {
			panic(fmt.Sprintf("duplicate route %s: %s vs %s", key, prev, endpointName(ep)))
		}
		seen[key] = endpointName(ep)
	}
}

// endpointName 端点展示名（查重 panic / 警告定位用）
func endpointName(ep *hinge.Endpoint) string {
	if ep.Owner != "" {
		return ep.Owner + "." + ep.Handler
	}
	return ep.Handler
}

// addOperation 为一个端点生成 OpenAPI 操作。
// 类型来源：ep.QType / ep.BType / ep.RType（生成表用 hinge.Type[T]() 填充；
// nil 视同 v0.1 的 interface 占位：无参数 / 无 body / 任意 JSON 响应）。
//   - Q：query 参数来自 `query` 标签，path 参数来自路径 {id}（Q 中 path 标签字段命中则用其类型），header/cookie 标签同 v0.1
//   - B：nil / interface{} 表示无 body，否则整包作为 application/json 请求体
//   - R：Data 段 schema；hinge.FileStream 输出二进制流并声明 404，接口类型（Empty/any）输出任意 JSON 值 schema
//   - 文档语义映射：ep.Deprecated → deprecated；ep.Status(0→200) → 成功码；
//     ep.Auth → security + 401；ep.Limit → x-rate-limit；ep.Timeout → x-timeout；ep.Envelope → 命名壳 schema
func addOperation(g *specGen, ep *hinge.Endpoint) {
	op := openapi3.NewOperation()
	// Responses 先初始化：Auth 401 等响应随后写入
	op.Responses = openapi3.NewResponses()
	op.Summary = ep.Summary
	op.Description = ep.Description
	op.OperationID = operationID(ep)
	op.Deprecated = ep.Deprecated
	op.Tags = dedupTags(ep.Tags)
	for _, tg := range op.Tags {
		g.noteTag(tg, "")
	}

	// 环绕拦截器（oapi:auth / oapi:limit / oapi:timeout 注解进 Endpoint）的文档语义
	if ep.Auth != "" {
		op.Security = &openapi3.SecurityRequirements{{ep.Auth: {}}}
		op.Responses.Set("401", &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription("Unauthorized：token 缺失或无效")})
	}
	if ep.Limit != "" {
		if op.Extensions == nil {
			op.Extensions = map[string]any{}
		}
		op.Extensions["x-rate-limit"] = ep.Limit
	}
	if ep.Timeout > 0 {
		if op.Extensions == nil {
			op.Extensions = map[string]any{}
		}
		op.Extensions["x-timeout"] = ep.Timeout.Seconds()
	}

	// operationID 查重 + Summary 缺失提示（正式轮）
	routeKey := ep.Method + " " + ep.Path
	if !g.probe {
		if prev, dup := g.opIDs[op.OperationID]; dup {
			g.warns = append(g.warns, fmt.Sprintf("duplicate operationID %q: %s vs %s（区分 Owner/Handler 消除）",
				op.OperationID, prev, routeKey))
		} else {
			g.opIDs[op.OperationID] = routeKey
		}
		if op.Summary == "" {
			g.warns = append(g.warns, "missing summary: "+routeKey)
		}
	}

	qT, bT, rT := ep.QType, ep.BType, ep.RType

	// path 参数：优先取 Q 中 path 标签字段的类型与描述（缺省回退 string）
	pathFields := pathFieldsOf(qT)
	for _, name := range pathParams(ep.Path) {
		p := openapi3.NewPathParameter(name)
		if f, ok := pathFields[name]; ok {
			p.Schema = paramSchema(f.Type)
			p.Schema = applyFieldTags(p.Schema, f)
			if f.Type.Kind() == reflect.Pointer && p.Schema.Value != nil {
				p.Schema.Value.Nullable = true
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
	if qT != nil {
		for _, p := range queryParams(qT) {
			op.AddParameter(p)
		}
	}

	if bT != nil && bT.Kind() != reflect.Interface {
		bt := bT
		for bt.Kind() == reflect.Pointer {
			bt = bt.Elem()
		}
		switch {
		case bt == rawBodyType:
			// RawBody：原始字节体（application/octet-stream + binary）
			bin := openapi3.NewStringSchema()
			bin.Format = "binary"
			op.RequestBody = &openapi3.RequestBodyRef{Value: openapi3.NewRequestBody().
				WithRequired(true).
				WithDescription("Raw request body").
				WithContent(openapi3.NewContentWithSchemaRef(&openapi3.SchemaRef{Value: bin}, []string{"application/octet-stream"}))}
		case hasFileHeader(bT):
			// multipart 文件表单
			sch := multipartSchema(bT)
			op.RequestBody = &openapi3.RequestBodyRef{Value: openapi3.NewRequestBody().
				WithRequired(true).
				WithDescription("Request body for " + bT.String()).
				WithContent(openapi3.NewContentWithSchemaRef(&openapi3.SchemaRef{Value: sch}, []string{"multipart/form-data"}))}
		default:
			ref := g.sb.ref(bT)
			op.RequestBody = &openapi3.RequestBodyRef{Value: openapi3.NewRequestBody().
				WithRequired(true).
				WithDescription("Request body for " + bT.String()).
				WithContent(openapi3.NewContentWithSchemaRef(ref, []string{"application/json"}))}
		}
	}

	// 成功响应码：ep.Status；0 → 200
	successCode := strconv.Itoa(ep.Status)
	if ep.Status == 0 {
		successCode = "200"
	}

	var successResp *openapi3.ResponseRef
	if rT == nil {
		// RType 未填（nil）：视同接口占位 → 任意 JSON 值
		successResp = okResponse(g, ep, &openapi3.SchemaRef{Value: openapi3.NewSchema()})
	} else {
		if rT.Kind() == reflect.Pointer {
			rT = rT.Elem()
		}
		// hinge.Response[R] 定制壳 → 取 Data 字段类型作为响应 schema
		if rT.Implements(respWrapperIface) {
			if f, ok := rT.FieldByName("Data"); ok {
				rT = f.Type
			}
		}
		switch {
		case rT == reflect.TypeOf(hinge.FileStream{}):
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
			successResp = okResponse(g, ep, &openapi3.SchemaRef{Value: openapi3.NewSchema()})
		default:
			successResp = okResponse(g, ep, g.sb.ref(rT))
		}
	}
	op.Responses.Set(successCode, successResp)

	// 合并到 Paths：同路径不同 method 共存
	p := g.doc.Paths.Value(ep.Path)
	if p == nil {
		p = &openapi3.PathItem{}
		g.doc.Paths.Set(ep.Path, p)
	}
	p.SetOperation(ep.Method, op)
}

// dedupTags 去重保序（手写端点表的 Tags 可能重复）
func dedupTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, t := range tags {
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// operationID 文档操作 ID：Owner_Handler（如 UserEp_GetUser）；
// Owner 为空时回退 method+path 清洗（如 GET /users/{id} → get_users_id）。
func operationID(ep *hinge.Endpoint) string {
	if ep.Owner != "" {
		return ep.Owner + "_" + ep.Handler
	}
	return fallbackOperationID(ep.Method, ep.Path)
}

// fallbackOperationID method+path 清洗为合法标识符：
// 小写；非 [a-z0-9] 的连续字符折叠为单个 _；去首尾 _。
func fallbackOperationID(method, path string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(method + " " + path) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore {
				b.WriteRune('_')
				prevUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

// effectiveEnvelope 返回端点实际生效的壳文档方案。
//   - ep.Envelope 命中 OptionWithEnvelopeSchemas 注册表 → 手写命名壳 schema（instance 为 nil）
//   - ep.Envelope 未命中 → 正式轮输出警告并退回默认推导链
//   - 默认推导链：OptionWithEnvelope 壳实例 > OptionWithEnvelopeSchema 手写壳 > 默认壳
//
// instance 非 nil 时从壳实例推导；否则 custom 非 nil 为手写壳 schema 函数。
func effectiveEnvelope(g *specGen, ep *hinge.Endpoint) (hinge.Envelope, EnvelopeSchema) {
	if ep.Envelope != "" {
		if fn, ok := envelopeSchemas[ep.Envelope]; ok {
			return nil, fn
		}
		if !g.probe {
			g.warns = append(g.warns, fmt.Sprintf("unknown envelope %q: %s %s（未通过 OptionWithEnvelopeSchemas 注册，退回默认壳推导）",
				ep.Envelope, ep.Method, ep.Path))
		}
	}
	if envelopeInstance != nil {
		return envelopeInstance, nil
	}
	if envelopeSchemaCustom {
		return nil, envelopeSchema
	}
	return hinge.DefaultEnvelope{}, nil
}

// okResponse 成功响应：壳形态由实际生效的壳方案推导（文档与运行时同构）。
func okResponse(g *specGen, ep *hinge.Endpoint, data *openapi3.SchemaRef) *openapi3.ResponseRef {
	env, custom := effectiveEnvelope(g, ep)
	var schema *openapi3.SchemaRef
	if env == nil {
		// 手写壳 schema：命名壳命中 / OptionWithEnvelopeSchema 且无壳实例
		schema = custom(data)
	} else {
		status := ep.Status
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

// deriveEnvelopeSchema 调用壳实例的 Success 取返回值，反射生成壳 schema：
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
			// 明细字段（运行时按需输出，omitempty）：不进入默认壳 schema，
			// 避免内部类型泄漏进所有生成的 spec 组件
			switch name {
			case "aggregated_error", "bind_errors":
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

// paramSchema 参数 schema：按 kind 反射。
// v0.2 参数绑定由 hinge gen 产出（请求期零反射），不存在 ParamBinder 文档特判。
func paramSchema(t reflect.Type) *openapi3.SchemaRef {
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
			if cname, cok := tagValueOf(f, "cookie"); cok {
				p := openapi3.NewCookieParameter(cname)
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
			p.Schema = applyFieldTags(p.Schema, f)
			if f.Type.Kind() == reflect.Pointer && p.Schema.Value != nil {
				p.Schema.Value.Nullable = true
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

var rawBodyType = reflect.TypeOf(hinge.RawBody(nil))

var fileHeaderT = reflect.TypeOf(hinge.FileHeader{})

// hasFileHeader 判断 B 是否声明了上传文件字段（含内嵌结构体递归）。
// 生成器据此推导 multipart 请求体 schema（语义与 v0.1 contract.HasFileHeader 一致）。
func hasFileHeader(t reflect.Type) bool {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && hasFileHeader(ft) {
				return true
			}
			continue
		}
		if isFileHeaderField(f.Type) {
			return true
		}
	}
	return false
}

func isFileHeaderField(t reflect.Type) bool {
	if t == fileHeaderT {
		return true
	}
	if t.Kind() == reflect.Pointer && t.Elem() == fileHeaderT {
		return true
	}
	if t.Kind() == reflect.Slice {
		e := t.Elem()
		if e == fileHeaderT {
			return true
		}
		if e.Kind() == reflect.Pointer && e.Elem() == fileHeaderT {
			return true
		}
	}
	return false
}

// multipartSchema 为含 FileHeader 字段的 B 生成 multipart/form-data 的 requestBody
// schema：FileHeader 字段 → {type: string, format: binary}；其余 form 标签字段 →
// 标量/切片 schema；binding:"required" 字段进入 required 列表。
// 仅扁平字段（不递归内嵌结构体）。
func multipartSchema(t reflect.Type) *openapi3.Schema {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	sch := openapi3.NewObjectSchema()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || f.Anonymous {
			continue
		}
		name := f.Tag.Get("form")
		if name == "" {
			continue
		}
		var ps *openapi3.Schema
		if isFileHeaderField(f.Type) {
			ps = openapi3.NewStringSchema()
			ps.Format = "binary"
		} else if ref := paramSchema(f.Type); ref != nil && ref.Value != nil {
			ps = ref.Value
		} else {
			ps = openapi3.NewStringSchema()
		}
		if sch.Properties == nil {
			sch.Properties = openapi3.Schemas{}
		}
		sch.Properties[name] = &openapi3.SchemaRef{Value: ps}
		if isRequiredOpenAPI(f) {
			sch.Required = append(sch.Required, name)
		}
	}
	return sch
}
