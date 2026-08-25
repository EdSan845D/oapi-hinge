//go:build openapi

// Package openapi 开发期 OpenAPI 文档生成器：从统一路由注册表生成 OpenAPI 3.1 规范。
// fuego 仅在本包作为文档生成工具使用（-tags openapi 构建），
// release 构建完全不包含本包，运行时零开发期依赖。
//
// 用法：go run -tags openapi . -out openapi.yaml
package openapi

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"fuego-hinge/internal/contract"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-fuego/fuego"
	"gopkg.in/yaml.v3"
)

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

// Option 文档生成选项：以函数式方式注入文档元信息
// （Info / Servers / SecuritySchemes 等，见 OptionWithXxx）。
type Option = func(*fuego.OpenAPI)

// Generate 生成 OpenAPI 文档并写入 out（.yaml/.yml -> YAML，.json -> JSON）。
// groups 为路由分组树（routes.All()）；opts 注入文档元信息。
func Generate(out string, groups []*contract.Group, opts ...Option) error {
	engine := fuego.NewEngine(fuego.WithOpenAPIConfig(fuego.OpenAPIConfig{
		DisableLocalSave: true,
		DisableMessages:  true,
		PrettyFormatJSON: true,
	}))

	oa := engine.OpenAPI

	checkDuplicates(groups)
	for _, g := range groups {
		addGroup(oa, g, "", nil)
	}

	for _, opt := range opts {
		opt(oa)
	}

	// 不调用 OutputOpenAPISpec()：其内部的 resolveSchemaRefs 对递归 schema
	// 存在无限递归 bug（fuego v0.20.0），会栈溢出。SchemaTagFromType 返回的 ref
	// 已带完整 Value，直接序列化描述树即可，输出同样包含干净的 $ref 与 components。
	doc := oa.Description()

	var data []byte
	var err error
	if strings.HasSuffix(strings.ToLower(out), ".json") {
		data, err = json.MarshalIndent(doc, "", "  ")
	} else {
		data, err = yaml.Marshal(doc)
	}
	if err != nil {
		return fmt.Errorf("marshal spec: %w", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	return nil
}

// addGroup 递归生成一个分组的所有 operation。
// inherited 为父组继承下来的中间件；组 Tags 与路由 Tags 合并。
func addGroup(oa *fuego.OpenAPI, g *contract.Group, prefix string, inherited []any) {
	path := prefix + g.Prefix
	mws := append(append([]any{}, inherited...), g.Middlewares...)
	for _, r := range g.Routes {
		addOperation(oa, r, path, g.Tags, mws)
	}
	for _, child := range g.Children {
		addGroup(oa, child, path, mws)
	}
}

// addOperation 为一个路由生成 OpenAPI 操作。
// Handler 模板：func(context.Context, Q, B) (R, error)
//   - Q：query 参数来自 `query` 标签，path 参数来自路径 {id}
//   - B：interface{} 表示无 body，否则整包作为 application/json 请求体
//   - R：Data 段 schema；*FileStream 输出二进制流，Empty 输出 null
//   - 中间件文档钩子按函数名匹配（见 RegisterMiddlewareDoc），可修改 operation
func addOperation(oa *fuego.OpenAPI, r contract.Route, groupPath string, groupTags []string, mws []any) {
	op := openapi3.NewOperation()
	op.Summary = r.Summary
	op.Description = r.Description
	op.OperationID = operationID(r.Handler)
	op.Tags = mergeTags(groupTags, r.Tags)
	applyHooks(op, mws)

	fn := reflect.TypeOf(r.Handler)
	qT, bT, rT := fn.In(1), fn.In(2), fn.Out(0)

	for _, name := range pathParams(r.Path) {
		p := openapi3.NewPathParameter(name)
		p.Schema = openapi3.NewStringSchema().NewRef()
		op.AddParameter(p)
	}
	for _, p := range queryParams(qT) {
		op.AddParameter(p)
	}

	if bT.Kind() != reflect.Interface {
		tag := fuego.SchemaTagFromType(oa, reflect.New(bT).Interface())
		op.RequestBody = &openapi3.RequestBodyRef{Value: openapi3.NewRequestBody().
			WithRequired(true).
			WithDescription("Request body for " + bT.String()).
			WithContent(openapi3.NewContentWithSchemaRef(&tag.SchemaRef, []string{"application/json"}))}
	}

	op.Responses = openapi3.NewResponses()
	op.Responses.Set("401", &openapi3.ResponseRef{Value: openapi3.NewResponse().
		WithDescription("Unauthorized：token 缺失或无效")})

	if rT.Kind() == reflect.Pointer {
		rT = rT.Elem()
	}
	switch {
	case rT == reflect.TypeOf(contract.FileStream{}):
		bin := openapi3.NewStringSchema()
		bin.Format = "binary"
		op.Responses.Set("200", &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription("文件二进制流").
			WithContent(openapi3.NewContentWithSchemaRef(&openapi3.SchemaRef{Value: bin}, []string{"application/octet-stream"}))})
		op.Responses.Set("404", &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription("文件不存在")})
	case rT == reflect.TypeOf(contract.Empty{}):
		nilSchema := openapi3.NewObjectSchema().WithNullable()
		nilSchema.Description = "无数据（null）"
		op.Responses.Set("200", okResponse(oa, &openapi3.SchemaRef{Value: nilSchema}))
	default:
		tag := fuego.SchemaTagFromType(oa, reflect.New(rT).Interface())
		op.Responses.Set("200", okResponse(oa, &tag.SchemaRef))
	}

	oa.Description().AddOperation(groupPath+r.Path, r.Method, op)
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

// okResponse 统一响应壳 {code, data, msg}
func okResponse(oa *fuego.OpenAPI, data *openapi3.SchemaRef) *openapi3.ResponseRef {
	env := openapi3.NewObjectSchema()
	env.Properties = openapi3.Schemas{
		"code": {Value: openapi3.NewIntegerSchema()},
		"data": data,
		"msg":  {Value: openapi3.NewStringSchema()},
	}
	return &openapi3.ResponseRef{Value: openapi3.NewResponse().
		WithDescription("OK").
		WithContent(openapi3.NewContentWithSchemaRef(&openapi3.SchemaRef{Value: env}, []string{"application/json"}))}
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
			if !f.IsExported() {
				continue
			}
			if f.Anonymous {
				walk(f.Type)
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
			p.Schema = &openapi3.SchemaRef{Value: schemaByKind(f.Type)}
			if dv, ok := f.Tag.Lookup("default"); ok && dv != "" {
				p.Schema.Value.Default = parseDefault(dv, f.Type.Kind())
			}
			if strings.Contains(f.Tag.Get("binding"), "required") {
				p.Required = true
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

func parseDefault(s string, kind reflect.Kind) any {
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v, err := strconv.Atoi(s); err == nil {
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

// operationID 取 Handler 函数名（如 handlers.ListUsers -> ListUsers）
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
