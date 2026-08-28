package contract

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Server 运行时适配器接口规范：各框架适配器（servergin / serverecho）
// 提供具体实现，业务装配层面向该接口编程时可互换适配器。
type Server interface {
	Mount(g any, groups []*Group)
}

func IsBodyMethod(method string) bool {
	return method == "POST" || method == "PUT" || method == "PATCH"
}

// CheckTarget 校验入参目标：接口类型占位（NoReq/any 等）返回 nil（validator.isNil 跳过）；
// 具体结构体返回其指针（与绑定阶段一致）
func CheckTarget(t reflect.Type, v reflect.Value) any {
	if t.Kind() == reflect.Interface {
		return nil
	}
	return v.Addr().Interface()
}

func NewValue(t reflect.Type) reflect.Value {
	if t.Kind() == reflect.Pointer {
		return reflect.New(t.Elem())
	}
	return reflect.New(t)
}

func TagValue(ft reflect.StructField, key string) (string, bool) {
	v := ft.Tag.Get(key)
	if v == "" {
		return "", false
	}
	return strings.Split(v, ",")[0], true
}

// timeType time.Time 类型（query/path/header 支持 RFC3339 绑定）
var timeType = reflect.TypeFor[time.Time]()

// 把原始字符串解析写入字段（query/path/header 共用）。
// 支持基本类型、指针（自动分配）、time.Time（RFC3339）与切片（逗号分隔或重复参数）；
// 声明了绑定标签但类型不支持时返回错误（避免静默丢值难以排查）。
func SetRaw(f reflect.Value, raw, name string) error {
	if raw == "" {
		return nil
	}
	// 指针：自动分配后绑定元素（*int / *string / *time.Time 等）
	for f.Kind() == reflect.Pointer {
		if f.IsNil() {
			f.Set(reflect.New(f.Type().Elem()))
		}
		f = f.Elem()
	}
	if f.Kind() == reflect.Slice {
		return SetSliceValue(f, []string{raw}, name)
	}
	return SetRawBasic(f, raw, name)
}

// 标量绑定（不含指针/切片解包）
func SetRawBasic(f reflect.Value, raw, name string) error {
	switch f.Kind() {
	case reflect.String:
		f.SetString(raw)
	case reflect.Bool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("invalid %s: %s", name, raw)
		}
		f.SetBool(v)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid %s: %s", name, raw)
		}
		f.SetInt(v)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid %s: %s", name, raw)
		}
		f.SetUint(v)
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("invalid %s: %s", name, raw)
		}
		f.SetFloat(v)
	case reflect.Struct:
		if f.Type() == timeType {
			v, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return fmt.Errorf("invalid %s: %s (want RFC3339)", name, raw)
			}
			f.Set(reflect.ValueOf(v))
			return nil
		}
		return fmt.Errorf("invalid %s: unsupported field type %s", name, f.Type())
	case reflect.Slice:
		return SetSliceValue(f, []string{raw}, name)
	default:
		return fmt.Errorf("invalid %s: unsupported field type %s", name, f.Type())
	}
	return nil
}

// 绑定切片字段：vals 为收集到的原始值（重复参数或逗号分隔展开后）。
// []string 保持原样；其他元素类型逐个解析，失败即报错。
func SetSliceValue(f reflect.Value, vals []string, name string) error {
	if f.Type().Elem().Kind() == reflect.String {
		f.Set(reflect.ValueOf(vals))
		return nil
	}
	// 逗号分隔展开：?ids=1,2,3 与 ?ids=1&ids=2 等价
	var expanded []string
	for _, v := range vals {
		expanded = append(expanded, strings.Split(v, ",")...)
	}
	slice := reflect.MakeSlice(f.Type(), 0, len(expanded))
	for _, v := range expanded {
		ev := reflect.New(f.Type().Elem()).Elem()
		if err := SetRawBasic(ev, v, name); err != nil {
			return err
		}
		slice = reflect.Append(slice, ev)
	}
	f.Set(slice)
	return nil
}
