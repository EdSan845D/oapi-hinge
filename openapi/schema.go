//go:build openapi

package openapi

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

// schemaBuilder 从 Go 类型反射生成 OpenAPI schema。
// 命名结构体注册到 components.schemas 并以 $ref 引用：组件去重 + 递归类型防栈溢出。
// 组件名采用两阶段分配：探测遍历收集全部类型 → assignNames 统一命名（跨包同名防碰撞）→ 正式构建。
type schemaBuilder struct {
	doc      *openapi3.T
	names    map[reflect.Type]string // 已注册（含注册中）的类型 -> 组件名
	building map[reflect.Type]bool   // 递归保护：正在构建组件的类型
	done     map[reflect.Type]bool   // 组件已构建（两阶段命名下 names 预分配 ≠ 已构建）
	seen     []reflect.Type          // 首次注册顺序（供两阶段命名的冲突分析）
}

// names 可为 nil（探测模式：即时命名，仅用于收集类型集合）；
// 正式构建传入 assignNames 预分配的命名表（跨包同名类型防碰撞）。
func newSchemaBuilder(doc *openapi3.T, names map[reflect.Type]string) *schemaBuilder {
	if names == nil {
		names = map[reflect.Type]string{}
	}
	return &schemaBuilder{doc: doc, names: names, building: map[reflect.Type]bool{}, done: map[reflect.Type]bool{}}
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
	// 注意：两阶段命名下 names 可能已预分配，但不能据此返回——组件体可能尚未构建；
	// 以 done/building 区分「已构建/构建中」，否则正式轮组件表为空
	if t.Kind() == reflect.Struct && t.Name() != "" {
		// 类型级 schema 覆盖（组件替换）：$ref 结构不变，组件内容 = 注册 schema。
		// 返回纯 $ref（Value 为 nil），下游 description/约束/注释按组件引用语义叠加。
		if s := typeSchemaFor(t); s != nil {
			name := b.names[t]
			if name == "" {
				name = schemaName(t)
				b.names[t] = name
				b.seen = append(b.seen, t)
			}
			b.done[t] = true
			if b.doc.Components == nil {
				b.doc.Components = &openapi3.Components{}
			}
			if b.doc.Components.Schemas == nil {
				b.doc.Components.Schemas = openapi3.Schemas{}
			}
			b.doc.Components.Schemas[name] = &openapi3.SchemaRef{Value: s}
			return &openapi3.SchemaRef{Ref: "#/components/schemas/" + name}
		}
		if b.done[t] || b.building[t] {
			return &openapi3.SchemaRef{Ref: "#/components/schemas/" + b.names[t]}
		}
		name := b.names[t]
		if name == "" {
			name = schemaName(t)
			b.names[t] = name
			b.seen = append(b.seen, t)
		}
		b.building[t] = true
		s := b.buildStruct(t)
		delete(b.building, t)
		b.done[t] = true
		// 结构体注释 → 组件描述（注释即文档）
		if td := typeDocsFor(t); td != nil && td.description != "" {
			if ref := runCommentParser(td.description, &openapi3.SchemaRef{Value: s}); ref != nil && ref.Value != nil {
				s = ref.Value
			}
		}
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
		// ① 约束 + example 标签（validate/binding → 约束；组件替换/ParamBinder 类型自动跳过）
		ref = applyFieldTags(ref, f)
		// ② 指针字段 → nullable（区分「未传」；$ref 字段不标）
		if f.Type.Kind() == reflect.Pointer && ref.Value != nil {
			ref.Value.Nullable = true
		}
		// ③ body 字段 default 标签 → schema 默认值
		if d, ok := f.Tag.Lookup("default"); ok && d != "" && ref.Value != nil {
			ref.Value.Default = parseDefault(d, derefKind(f.Type))
		}
		// ④ description 标签
		if d, ok := f.Tag.Lookup("description"); ok && d != "" {
			ref = withDescription(ref, d)
		}
		// ⑤ 字段注释（description 标签优先，内置解析器尊重已有描述）
		ref = applyFieldComment(ref, t, f.Name)
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

// schemaName 组件裸名：类型名（泛型参数取末段限定名扁平化 + 非法字符清洗）。
// 例：User -> User；Paged[github.com/x/handlers.User] -> Paged_handlers.User。
// 泛型实例的 reflect Name() 含类型参数完整路径（含 /），必须压缩与清洗才能用作组件名。
func schemaName(t reflect.Type) string {
	name := t.Name()
	if i := strings.Index(name, "["); i >= 0 {
		origin := name[:i]
		args := name[i+1 : len(name)-1] // 去首尾 []
		var parts []string
		for _, a := range strings.Split(args, ",") {
			a = strings.TrimSpace(a)
			a = a[strings.LastIndex(a, "/")+1:] // 类型参数取末段限定名（handlers.User）
			parts = append(parts, a)
		}
		name = origin + "_" + strings.Join(parts, "_")
	}
	name = sanitizeIdent(name)
	if name == "" {
		return "Anonymous"
	}
	return name
}

// sanitizeIdent 组件名合法字符清洗：仅保留 [a-zA-Z0-9_-]，其余（含点号——
// kin-openapi 内存态校验对带点组件名的引用解析有问题）替换为 _。
func sanitizeIdent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// assignNames 两阶段命名的第二阶段：为收集到的全部组件类型分配最终组件名。
// 策略：裸名优先；发现冲突（跨包同名，或裸名已被先前升级占用）时，冲突方整体升级为
// 「末段包名_裸名」，候选仍冲突则加深包路径段，最终数字后缀兜底。
// 全程按首次出现顺序处理，输出确定；升级时返回警告。
func assignNames(types []reflect.Type) (map[reflect.Type]string, []string) {
	final := make(map[reflect.Type]string, len(types))
	used := map[string]bool{} // 已占用的最终组件名（含升级名）

	var order []string
	groups := map[string][]reflect.Type{}
	for _, t := range types {
		bare := schemaName(t)
		if _, ok := groups[bare]; !ok {
			order = append(order, bare)
		}
		groups[bare] = append(groups[bare], t)
	}

	var warns []string
	for _, bare := range order {
		ts := groups[bare]
		if len(ts) == 1 && !used[bare] {
			final[ts[0]] = bare
			used[bare] = true
			continue
		}
		// 冲突：跨包同名（或裸名已被先前升级占用）——冲突方整体升级，
		// 避免「后注册者改名」的不确定性
		assigned := false
		for seg := 1; seg <= 8 && !assigned; seg++ {
			cand := make(map[reflect.Type]string, len(ts))
			taken := map[string]bool{}
			ok := true
			for _, t := range ts {
				cn := pkgPrefix(t, seg) + "_" + bare
				if cn == bare || taken[cn] || used[cn] {
					ok = false
					break
				}
				taken[cn] = true
				cand[t] = cn
			}
			if !ok {
				continue
			}
			var upgraded []string
			for _, t := range ts {
				final[t] = cand[t]
				used[cand[t]] = true
				upgraded = append(upgraded, cand[t])
			}
			warns = append(warns, fmt.Sprintf("schema name collision on %q -> %s（组件名已升级，引用已同步）",
				bare, strings.Join(upgraded, ", ")))
			assigned = true
		}
		if !assigned { // 数字兜底（8 级包路径仍冲突的理论情况）
			for i, t := range ts {
				final[t] = fmt.Sprintf("%s_%d", bare, i+1)
				used[final[t]] = true
			}
			warns = append(warns, "schema name collision on "+bare+" -> 数字后缀兜底")
		}
	}
	return final, warns
}

// pkgPrefix 取类型包路径的末 seg 段，"_" 连接（如 app/handlers, seg=1 -> "handlers"）
func pkgPrefix(t reflect.Type, seg int) string {
	p := t.PkgPath()
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	if seg > len(parts) {
		seg = len(parts)
	}
	return strings.Join(parts[len(parts)-seg:], "_")
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
