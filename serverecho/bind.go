package serverecho

import (
	"errors"
	"reflect"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/contract/response"

	"github.com/labstack/echo/v4"
)

// bindQueryPath 按预解析的字段元数据绑定（query/form/path/header/cookie 标签）。
// 元数据由 contract.ParseFields 挂载期解析并缓存（与框架无关），此处只做框架取值。
// 必填校验统一在 validator.Run 执行（binding/validate 双标签），此处不再重复。
func bindQueryPath(c echo.Context, req any) error {
	rv := reflect.ValueOf(req)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return errors.New("invalid params type")
	}
	return bindFields(c, rv.Elem(), contract.ParseFields(rv.Elem().Type()))
}

// bindFields 按元数据逐字段绑定（框架侧只负责从请求取原始值）。
// 字段级错误不快速失败：解析失败的字段逐个收集（response.BindFieldError），
// 聚合为 response.BindError 返回，供响应壳输出 bind_errors 明细。
func bindFields(c echo.Context, e reflect.Value, metas []contract.FieldMeta) error {
	var fieldErrs []response.BindFieldError
	fieldErr := func(name, in string, err error) {
		fieldErrs = append(fieldErrs, response.BindFieldError{Field: name, In: in, Msg: err.Error()})
	}
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
				var be *response.BindError
				if errors.As(err, &be) {
					fieldErrs = append(fieldErrs, be.Fields...)
					continue
				}
				return err
			}
			continue
		}
		// 自定义绑定器：注册过的字段类型接管绑定（原始字符串 → 字段值，
		// 可改变类型形态：逗号串→命名切片、ID→缓存实体）。
		// 参数缺失时字段保持零值（required 由 validator.Run 兜底）。
		if binder, ok := contract.BinderFor(f.Type()); ok {
			src := collectRawValues(c, m)
			if len(src) == 0 {
				continue
			}
			v, err := binder(src)
			if err != nil {
				// 携带状态码的业务错误（如 404）保持快速失败，语义不因聚合而改变
				if _, _, _, ok := contract.ResolveErrorStatus(err); ok {
					return err
				}
				name, in := m.Source()
				fieldErr(name, in, err)
				continue
			}
			f.Set(reflect.ValueOf(v))
			continue
		}
		name, in := m.Source()
		switch in {
		case "path":
			if err := contract.SetRaw(f, c.Param(name), name); err != nil {
				fieldErr(name, in, err)
				continue
			}
		case "header":
			if err := contract.SetRaw(f, c.Request().Header.Get(name), name); err != nil {
				fieldErr(name, in, err)
				continue
			}
		case "cookie":
			ck, err := c.Cookie(name)
			raw := ""
			if err == nil && ck != nil {
				raw = ck.Value // cookie 缺失按空处理：default 标签填充，required 交给 validator.Run
			}
			if err := contract.SetRaw(f, raw, name); err != nil {
				fieldErr(name, in, err)
				continue
			}
			if m.Def != "" && f.IsZero() {
				if err := contract.SetRaw(f, m.Def, name); err != nil {
					fieldErr(name, in, err)
					continue
				}
			}
		case "query", "form":
			if err := setValue(c, f, name, false); err != nil {
				fieldErr(name, in, err)
				continue
			}
			// default 标签：字段未绑定到值时填充运行时默认值（与文档 default 同步）
			if m.Def != "" && f.IsZero() {
				if err := contract.SetRaw(f, m.Def, name); err != nil {
					fieldErr(name, in, err)
					continue
				}
			}
		}
	}
	if len(fieldErrs) > 0 {
		return &response.BindError{Fields: fieldErrs}
	}
	return nil
}

// setValue 从 query/path 取值并写入字段（echo 取值 API）
func setValue(c echo.Context, f reflect.Value, name string, path bool) error {
	var raw string
	if path {
		raw = c.Param(name)
	} else {
		raw = c.QueryParam(name)
	}
	// 多值 query（?tag=a&tag=b）→ 切片；非字符串元素逐个解析（?ids=1&ids=2）
	if f.Kind() == reflect.Slice && !path {
		if vals := c.QueryParams()[name]; len(vals) > 0 {
			return contract.SetSliceValue(f, vals, name)
		}
	}
	return contract.SetRaw(f, raw, name)
}

// collectRawValues 收集字段的原始参数值列表（供自定义绑定器消费）。
// 单值参数返回长度 1；重复参数 ?ids=1&ids=2 返回多值；缺失返回 nil。
func collectRawValues(c echo.Context, m contract.FieldMeta) []string {
	name, in := m.Source()
	switch in {
	case "path":
		if v := c.Param(name); v != "" {
			return []string{v}
		}
	case "header":
		if vs := c.Request().Header.Values(name); len(vs) > 0 {
			return vs
		}
	case "cookie":
		if ck, err := c.Cookie(name); err == nil && ck != nil && ck.Value != "" {
			return []string{ck.Value}
		}
	case "query", "form":
		return c.QueryParams()[name]
	}
	return nil
}
