//go:build openapi

package openapi

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// ============ 约束映射：validate/binding 标签 → OpenAPI 约束 ============
//
// 「约束即文档」：校验标签改了文档跟着变，消灭双写漂移。
// 双标签兼容：validate 与 binding 同等参与；同时存在时 validate 优先（同 key 不覆盖）。
// 未知标签静默忽略（dive/structonly 等结构性指令不映射）。
// 映射按字段类型分流：字符串 → 长度约束；数字 → 值约束；切片 → 项数约束。

// fieldConstraints 单个字段的约束集合。
type fieldConstraints struct {
	minLength *uint64
	maxLength *uint64
	minItems  *uint64
	maxItems  *uint64
	min       *float64
	max       *float64
	exclMin   *float64
	exclMax   *float64
	enum      []any
	format    string
}

// applyFieldTags 把字段标签（约束 + example）叠加到字段 schema 上。
// $ref（Value 为 nil）不叠加（避免污染共享/组件 schema）。
// v0.2：ParamBinder 注册表已随反射绑定移除，参数类型一律按标量反射输出。
func applyFieldTags(sch *openapi3.SchemaRef, sf reflect.StructField) *openapi3.SchemaRef {
	if sch == nil || sch.Value == nil {
		return sch
	}
	kind := derefKind(sf.Type)

	// ① 约束：validate 为主，binding 兜底
	c := parseConstraints(sf.Tag.Get("binding"), kind)
	c.merge(parseConstraints(sf.Tag.Get("validate"), kind))
	c.applyTo(sch.Value)

	// ② example 标签（按 kind 转型；注释解析器随后可覆盖）
	if ex := sf.Tag.Get("example"); ex != "" {
		sch.Value.Example = coerceExample(ex, kind)
	}
	return sch
}

// parseConstraints 解析单个标签值为约束集合。
func parseConstraints(tag string, kind reflect.Kind) *fieldConstraints {
	c := &fieldConstraints{}
	if tag == "" || tag == "-" {
		return c
	}
	for _, tok := range strings.Split(tag, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		key, val := tok, ""
		if i := strings.Index(tok, "="); i >= 0 {
			key, val = tok[:i], tok[i+1:]
		}
		switch key {
		case "required", "omitempty", "omitzero", "dive", "structonly", "-":
			// required 由 validator 语义单独处理；结构性指令不映射
		case "oneof":
			c.enum = coerceEnum(strings.Fields(val), kind)
		case "min", "gte":
			assignBound(c, kind, val, true, false)
		case "max", "lte":
			assignBound(c, kind, val, false, false)
		case "gt":
			assignBound(c, kind, val, true, true)
		case "lt":
			assignBound(c, kind, val, false, true)
		case "email":
			if kind == reflect.String {
				c.format = "email"
			}
		case "url":
			if kind == reflect.String {
				c.format = "uri"
			}
		case "datetime":
			if kind == reflect.String && isDateTimeLayout(val) {
				c.format = "date-time"
			}
		}
	}
	return c
}

// merge 合并另一组约束（other 的已设字段覆盖 c）。
func (c *fieldConstraints) merge(other *fieldConstraints) {
	if other == nil {
		return
	}
	if other.minLength != nil {
		c.minLength = other.minLength
	}
	if other.maxLength != nil {
		c.maxLength = other.maxLength
	}
	if other.minItems != nil {
		c.minItems = other.minItems
	}
	if other.maxItems != nil {
		c.maxItems = other.maxItems
	}
	if other.min != nil {
		c.min = other.min
	}
	if other.max != nil {
		c.max = other.max
	}
	if other.exclMin != nil {
		c.exclMin = other.exclMin
	}
	if other.exclMax != nil {
		c.exclMax = other.exclMax
	}
	if len(other.enum) > 0 {
		c.enum = other.enum
	}
	if other.format != "" {
		c.format = other.format
	}
}

// applyTo 叠加到 schema（不覆盖 schema 上已有的非零值）。
func (c *fieldConstraints) applyTo(s *openapi3.Schema) {
	if c.minLength != nil {
		s.MinLength = *c.minLength
	}
	if c.maxLength != nil {
		s.MaxLength = c.maxLength
	}
	if c.minItems != nil {
		s.MinItems = *c.minItems
	}
	if c.maxItems != nil {
		s.MaxItems = c.maxItems
	}
	if c.min != nil {
		s.Min = c.min
	}
	if c.max != nil {
		s.Max = c.max
	}
	if c.exclMin != nil {
		s.ExclusiveMin = openapi3.ExclusiveBound{Value: c.exclMin}
	}
	if c.exclMax != nil {
		s.ExclusiveMax = openapi3.ExclusiveBound{Value: c.exclMax}
	}
	if len(c.enum) > 0 {
		s.Enum = c.enum
	}
	if c.format != "" && s.Format == "" {
		s.Format = c.format
	}
}

// assignBound min/gte（下界）与 max/lte（上界）的按类型映射；exclusive 时数字走 Exclusive、长度取邻值。
func assignBound(c *fieldConstraints, kind reflect.Kind, val string, isMin, exclusive bool) {
	switch {
	case isStringKind(kind):
		n, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			return
		}
		if exclusive { // 长度严格不等：取邻值（>N → minLength=N+1；<N → maxLength=N-1）
			if isMin {
				v := n + 1
				c.minLength = &v
			} else if n > 0 {
				v := n - 1
				c.maxLength = &v
			}
			return
		}
		if isMin {
			c.minLength = &n
		} else {
			c.maxLength = &n
		}
	case isNumberKind(kind):
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return
		}
		v := f
		switch {
		case isMin && exclusive:
			c.exclMin = &v
		case isMin:
			c.min = &v
		case exclusive:
			c.exclMax = &v
		default:
			c.max = &v
		}
	case isSliceKind(kind):
		n, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			return
		}
		if exclusive { // 项数严格不等：取邻值
			if isMin {
				v := n + 1
				c.minItems = &v
			} else if n > 0 {
				v := n - 1
				c.maxItems = &v
			}
			return
		}
		if isMin {
			c.minItems = &n
		} else {
			c.maxItems = &n
		}
	}
}

// coerceEnum oneof 选项按字段类型转型。
func coerceEnum(opts []string, kind reflect.Kind) []any {
	out := make([]any, 0, len(opts))
	for _, o := range opts {
		switch {
		case kind == reflect.String:
			out = append(out, o)
		case isIntKind(kind):
			if v, err := strconv.ParseInt(o, 10, 64); err == nil {
				out = append(out, v)
			}
		case isUintKind(kind):
			if v, err := strconv.ParseUint(o, 10, 64); err == nil {
				out = append(out, v)
			}
		case isFloatKind(kind):
			if v, err := strconv.ParseFloat(o, 64); err == nil {
				out = append(out, v)
			}
		case kind == reflect.Bool:
			if v, err := strconv.ParseBool(o); err == nil {
				out = append(out, v)
			}
		default:
			out = append(out, o)
		}
	}
	return out
}

// coerceExample example 标签按字段类型转型（转型失败保留字符串）。
func coerceExample(s string, kind reflect.Kind) any {
	switch {
	case kind == reflect.String:
		return s
	case isIntKind(kind):
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	case isUintKind(kind):
		if v, err := strconv.ParseUint(s, 10, 64); err == nil {
			return v
		}
	case isFloatKind(kind):
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v
		}
	case kind == reflect.Bool:
		if v, err := strconv.ParseBool(s); err == nil {
			return v
		}
	}
	return s
}

func isStringKind(k reflect.Kind) bool { return k == reflect.String }

func isIntKind(k reflect.Kind) bool {
	return k == reflect.Int || k == reflect.Int8 || k == reflect.Int16 || k == reflect.Int32 || k == reflect.Int64
}

func isUintKind(k reflect.Kind) bool {
	return k == reflect.Uint || k == reflect.Uint8 || k == reflect.Uint16 || k == reflect.Uint32 || k == reflect.Uint64
}

func isFloatKind(k reflect.Kind) bool { return k == reflect.Float32 || k == reflect.Float64 }

func isNumberKind(k reflect.Kind) bool { return isIntKind(k) || isUintKind(k) || isFloatKind(k) }

func isSliceKind(k reflect.Kind) bool { return k == reflect.Slice || k == reflect.Array }

func isDateTimeLayout(layout string) bool {
	// RFC3339 族布局：含日期与时分秒模板；其余布局不映射（避免误标 date-time）
	return strings.Contains(layout, "2006") && strings.Contains(layout, "15:04")
}

func derefKind(t reflect.Type) reflect.Kind {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return reflect.Invalid
	}
	return t.Kind()
}
