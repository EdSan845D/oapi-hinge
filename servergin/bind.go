package servergin

import (
	"errors"
	"reflect"

	"github.com/EdSan845D/oapi-hinge/contract"

	"github.com/gin-gonic/gin"
)

// bindQueryPath 按预解析的字段元数据绑定（query/form/path/header 标签）。
// 元数据由 contract.ParseFields 挂载期解析并缓存（与框架无关），此处只做框架取值。
// 必填校验统一在 validator.Run 执行（binding/validate 双标签），此处不再重复。
func bindQueryPath(c *gin.Context, req any) error {
	rv := reflect.ValueOf(req)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return errors.New("invalid params type")
	}
	return bindFields(c, rv.Elem(), contract.ParseFields(rv.Elem().Type()))
}

// bindFields 按元数据逐字段绑定（框架侧只负责从请求取原始值）
func bindFields(c *gin.Context, e reflect.Value, metas []contract.FieldMeta) error {
	for _, m := range metas {
		f := e.Field(m.Index)
		sub := f
		if len(m.Children) > 0 {
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
			if err := bindFields(c, sub, m.Children); err != nil {
				return err
			}
			continue
		}
		// 自定义绑定器：注册过的字段类型接管绑定
		// 原始字符串 → 字段值，可改变类型形态：逗号串→命名切片、ID→缓存实体
		// 参数缺失时字段保持零值（required 由 validator.Run 兜底）。
		if binder, ok := contract.BinderFor(f.Type()); ok {
			src := collectRawValues(c, m)
			if len(src) == 0 {
				continue
			}
			v, err := binder(src)
			if err != nil {
				return err
			}
			f.Set(reflect.ValueOf(v))
			continue
		}
		if m.Path != "" {
			if err := contract.SetRaw(f, c.Param(m.Path), m.Path); err != nil {
				return err
			}
			continue
		}
		// header 标签优先（独立于 query/form）
		if m.Header != "" {
			if err := contract.SetRaw(f, c.GetHeader(m.Header), m.Header); err != nil {
				return err
			}
			continue
		}
		if m.Query != "" || m.Form != "" {
			name := m.Query
			if name == "" {
				name = m.Form
			}
			if err := setValue(c, f, name, false); err != nil {
				return err
			}
			// default 标签：字段未绑定到值时填充运行时默认值（与文档 default 同步）
			if m.Def != "" && f.IsZero() {
				if err := contract.SetRaw(f, m.Def, name); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// setValue 从 query/path 取值并写入字段（gin 取值 API）
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

// collectRawValues 收集字段的原始参数值列表（供自定义绑定器消费）。
// 单值参数返回长度 1；重复参数 ?ids=1&ids=2 返回多值；缺失返回 nil。
func collectRawValues(c *gin.Context, m contract.FieldMeta) []string {
	switch {
	case m.Path != "":
		if v := c.Param(m.Path); v != "" {
			return []string{v}
		}
	case m.Header != "":
		if v := c.GetHeader(m.Header); v != "" {
			return []string{v}
		}
	case m.Query != "" || m.Form != "":
		name := m.Query
		if name == "" {
			name = m.Form
		}
		return c.QueryArray(name)
	}
	return nil
}
