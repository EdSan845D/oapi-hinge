package openapi

import (
	"reflect"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

// schemaBuilder 从 Go 类型反射生成 OpenAPI schema。
// 命名结构体注册到 components.schemas 并以 $ref 引用：组件去重 + 递归类型防栈溢出。
type schemaBuilder struct {
	doc      *openapi3.T
	names    map[reflect.Type]string // 已注册（含注册中）的类型 -> 组件名
	building map[reflect.Type]bool   // 递归保护：正在构建组件的类型
}

func newSchemaBuilder(doc *openapi3.T) *schemaBuilder {
	return &schemaBuilder{doc: doc, names: map[reflect.Type]string{}, building: map[reflect.Type]bool{}}
}

var timeType = reflect.TypeOf(time.Time{})

// ref 返回类型的 schema 引用；命名结构体注册为组件。
func (b *schemaBuilder) ref(t reflect.Type) *openapi3.SchemaRef {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	// 知名内建类型：内联输出
	if t == timeType {
		return &openapi3.SchemaRef{Value: openapi3.NewStringSchema().WithFormat("date-time")}
	}

	// 命名结构体：组件化（先占位再构建，递归命中直接返回 ref）
	if t.Kind() == reflect.Struct && t.Name() != "" {
		if name, ok := b.names[t]; ok {
			return &openapi3.SchemaRef{Ref: "#/components/schemas/" + name}
		}
		name := schemaName(t)
		b.names[t] = name
		b.building[t] = true
		s := b.buildStruct(t)
		delete(b.building, t)
		if b.doc.Components == nil {
			b.doc.Components = &openapi3.Components{}
		}
		if b.doc.Components.Schemas == nil {
			b.doc.Components.Schemas = openapi3.Schemas{}
		}
		b.doc.Components.Schemas[name] = &openapi3.SchemaRef{Value: s}
		return &openapi3.SchemaRef{Ref: "#/components/schemas/" + name}
	}

	return &openapi3.SchemaRef{Value: b.build(t)}
}

// build 生成内联 schema（基本类型 / 容器 / 匿名结构体）
func (b *schemaBuilder) build(t reflect.Type) *openapi3.Schema {
	switch t.Kind() {
	case reflect.String:
		return openapi3.NewStringSchema()
	case reflect.Bool:
		return openapi3.NewBoolSchema()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return openapi3.NewIntegerSchema()
	case reflect.Int64, reflect.Uint64:
		return openapi3.NewIntegerSchema().WithFormat("int64")
	case reflect.Float32, reflect.Float64:
		return openapi3.NewFloat64Schema()
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 { // []byte -> binary
			return openapi3.NewStringSchema().WithFormat("binary")
		}
		arr := openapi3.NewArraySchema()
		arr.Items = b.ref(t.Elem())
		return arr
	case reflect.Map:
		obj := openapi3.NewObjectSchema()
		obj.AdditionalProperties.Schema = b.ref(t.Elem())
		return obj
	case reflect.Pointer:
		return b.build(t.Elem())
	case reflect.Struct: // 匿名结构体：内联对象
		return b.buildStruct(t)
	default: // interface{} / 其他：退化为 string
		return openapi3.NewStringSchema()
	}
}

// buildStruct 生成对象 schema（json tag 命名 + required + 内嵌结构体展平）
func (b *schemaBuilder) buildStruct(t reflect.Type) *openapi3.Schema {
	s := openapi3.NewObjectSchema()
	s.Properties = openapi3.Schemas{}
	var required []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Anonymous {
			sub := b.ref(f.Type)
			if sub.Value != nil && len(sub.Value.Properties) > 0 {
				for k, v := range sub.Value.Properties {
					s.Properties[k] = v
				}
				required = append(required, sub.Value.Required...)
			}
			continue
		}
		name, omit := jsonName(f)
		if name == "" || name == "-" {
			continue
		}
		ref := b.ref(f.Type)
		if d, ok := f.Tag.Lookup("description"); ok && d != "" {
			ref = withDescription(ref, d)
		}
		s.Properties[name] = ref
		// required 规则：json 未标 omitempty，或 binding 显式 required
		if !omit || isRequiredOpenAPI(f) {
			required = append(required, name)
		}
	}
	if len(required) > 0 {
		s.Required = required
	}
	return s
}

// schemaName 组件名：反射完整名（含包路径与泛型参数）转合法标识符。
// 例：handlers.User -> handlers_User；response.Paged[handlers.User] -> response_Paged_handlers_User
func schemaName(t reflect.Type) string {
	name := t.Name() // 泛型实例形如 Paged[User]
	name = strings.ReplaceAll(name, "[", "_")
	name = strings.ReplaceAll(name, "]", "")
	if name == "" {
		return "Anonymous"
	}
	return name
}

// jsonName 解析 json tag：返回字段名与是否 omitempty
func jsonName(f reflect.StructField) (name string, omit bool) {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	for _, p := range parts[1:] {
		if p == "omitempty" {
			omit = true
		}
	}
	return name, omit
}

// withDescription 给 schema 引用附加描述：内联 schema 直接设置；
// $ref 用 AllOf 包装（OpenAPI 不允许在引用旁直接放描述）。
func withDescription(ref *openapi3.SchemaRef, d string) *openapi3.SchemaRef {
	if ref.Value != nil {
		ref.Value.Description = d
		return ref
	}
	return &openapi3.SchemaRef{Value: &openapi3.Schema{Description: d, AllOf: []*openapi3.SchemaRef{ref}}}
}

// isRequiredOpenAPI 字段是否声明必填（binding / validate 双标签兼容。
// 与 contract/validator 的必填标签规则保持一致）。
func isRequiredOpenAPI(f reflect.StructField) bool {
	return strings.Contains(f.Tag.Get("binding"), "required") ||
		strings.Contains(f.Tag.Get("validate"), "required")
}
