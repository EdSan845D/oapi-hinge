package servergin

import (
	"errors"
	"reflect"
	"sync"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/gin-gonic/gin"
)

// fieldMeta 绑定字段元数据（挂载期解析一次，请求期零反射）
type fieldMeta struct {
	index    int
	kind     reflect.Kind
	path     string
	query    string
	form     string
	header   string
	def      string // default 标签：绑定缺失时的运行时默认值（与文档 default 同步生效）
	required bool
	children []fieldMeta
}

// bindCache Q 类型 -> 字段元数据缓存
var bindCache sync.Map

// parseFields 反射解析结构体字段元数据（内嵌结构体递归展平）
func parseFields(t reflect.Type) []fieldMeta {
	if v, ok := bindCache.Load(t); ok {
		return v.([]fieldMeta)
	}
	var out []fieldMeta
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				out = append(out, fieldMeta{children: parseFields(ft)})
			}
			continue
		}
		if !f.IsExported() {
			continue
		}
		var m fieldMeta
		m.index = i
		m.kind = f.Type.Kind()
		m.path, _ = contract.TagValue(f, "path")
		m.query, _ = contract.TagValue(f, "query")
		m.form, _ = contract.TagValue(f, "form")
		m.header, _ = contract.TagValue(f, "header")
		m.def = f.Tag.Get("default")
		out = append(out, m)
	}
	bindCache.Store(t, out)
	return out
}

// bindQueryPath 按预解析的字段元数据绑定（query/form/path/header 标签）。
// 必填校验统一在 validator.Run 执行（binding/validate 双标签），此处不再重复。
func bindQueryPath(c *gin.Context, req any) error {
	rv := reflect.ValueOf(req)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return errors.New("invalid params type")
	}
	return bindFields(c, rv.Elem(), parseFields(rv.Elem().Type()))
}

// bindFields 按元数据逐字段绑定
func bindFields(c *gin.Context, e reflect.Value, metas []fieldMeta) error {
	for _, m := range metas {
		f := e.Field(m.index)
		sub := f
		if len(m.children) > 0 {
			if sub.Kind() == reflect.Pointer {
				if sub.IsNil() {
					if !sub.CanSet() {
						// 未导出内嵌的指针字段不可写（反射 RO），跳过而非 panic
						continue
					}
					sub.Set(reflect.New(sub.Type().Elem()))
				}
				sub = sub.Elem()
			}
			if err := bindFields(c, sub, m.children); err != nil {
				return err
			}
			continue
		}
		if m.path != "" {
			if err := contract.SetRaw(f, c.Param(m.path), m.path); err != nil {
				return err
			}
			continue
		}
		// 逃生舱 1：header 标签优先（独立于 query/form）
		if m.header != "" {
			if err := contract.SetRaw(f, c.GetHeader(m.header), m.header); err != nil {
				return err
			}
			continue
		}
		if m.query != "" || m.form != "" {
			name := m.query
			if name == "" {
				name = m.form
			}
			if err := setValue(c, f, name, false); err != nil {
				return err
			}
			// default 标签：字段未绑定到值时填充运行时默认值（与文档 default 同步）
			if m.def != "" && f.IsZero() {
				if err := contract.SetRaw(f, m.def, name); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// setValue 从 query/path 取值并写入字段
func setValue(c *gin.Context, f reflect.Value, name string, path bool) error {
	raw := c.Query(name)
	if path {
		raw = c.Param(name)
	}
	// 多值 query（?tag=a&tag=b）→ 切片；非字符串元素逐个解析（?ids=1&ids=2）
	if f.Kind() == reflect.Slice && !path {
		if vals := c.QueryArray(name); len(vals) > 0 {
			return contract.SetSliceValue(f, vals, name)
		}
	}
	return contract.SetRaw(f, raw, name)
}
