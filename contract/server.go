package contract

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EdSan845D/oapi-hinge/contract/response"
)

// Server 运行时适配器接口规范：各框架适配器（servergin / serverecho）
// 提供具体实现，业务装配层面向该接口编程时可互换适配器。
// 作为 server 实现的示范存在：适配器可参考其形态提供自己的 Mount 签名。
type Server interface {
	Mount(g any, groups []*Group)
}

// ============ 错误解析默认策略（适配器直接使用，也可自行实现覆盖） ============
// 与响应壳/绑定公用件同层：默认实现集中在此，适配器保持薄封装。

// ResolveErrorStatus 提取错误自带的状态信息（StatusError → StatusCoder）。
// ok=false 表示普通错误，由调用方决定兑底策略（业务错误走 errorMapper，绑定错误走 bindStatus）。
func ResolveErrorStatus(err error) (status, code int, msg string, ok bool) {
	if se, e := errors.AsType[*StatusError](err); e {
		status = se.StatusCode()
		code = se.Code
		if code == 0 {
			if status == http.StatusOK {
				code = response.CodeError
			} else {
				code = status
			}
		}
		msg = se.Msg
		if msg == "" {
			msg = err.Error()
		}
		return status, code, msg, true
	}
	if sc, e := errors.AsType[StatusCoder](err); e {
		status = sc.StatusCode()
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return status, response.CodeError, err.Error(), true
	}
	return 0, 0, "", false
}

// ResolveError 业务错误解析：错误自带状态码优先，否则调用 mapError 兑底
// （mapError 由适配器注入，Server.SetErrorMapper；传 nil 则用 DefaultErrorMapper）。
func ResolveError(mapError func(err error) (httpStatus, bizCode int), err error) (int, int, string) {
	if status, code, msg, ok := ResolveErrorStatus(err); ok {
		return status, code, msg
	}
	if mapError == nil {
		mapError = DefaultErrorMapper
	}
	status, code := mapError(err)
	return status, code, err.Error()
}

// DefaultErrorMapper 默认兑底映射：ErrNotFound → 404；其余业务错误 → HTTP 200 + code=7。
func DefaultErrorMapper(err error) (httpStatus, bizCode int) {
	if errors.Is(err, ErrNotFound) {
		return http.StatusNotFound, http.StatusNotFound
	}
	return http.StatusOK, response.CodeError
}

// BindFail 绑定/校验失败响应的 (status, code)：默认 200 + CodeError（存量行为）；
// 自定义非 200 状态码时 code 跟随状态码（与 StatusError 约定一致）。
func BindFail(status int) (int, int) {
	if status <= 0 || status == http.StatusOK {
		return http.StatusOK, response.CodeError
	}
	return status, status
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

// FieldMeta 绑定字段元数据（挂载期解析一次，请求期零反射）。
// 与框架无关：path/query/form/header/default 标签由各框架适配器按同一语义消费，
// 框架侧只保留"从请求取原始值"的取值函数（如 gin 的 c.Query / echo 的 c.QueryParam）。
type FieldMeta struct {
	Index    int
	Kind     reflect.Kind
	Path     string
	Query    string
	Form     string
	Header   string
	Cookie   string
	Def      string      // default 标签：绑定缺失时的运行时默认值（与文档 default 同步生效）
	Children []FieldMeta // 内嵌结构体递归展平
}

// Source 返回字段的取值来源与参数名（用于字段级错误定位）。
// 多标签共存时优先级：path > header > cookie > query/form；无标签返回空（body 字段）。
func (m FieldMeta) Source() (name, in string) {
	switch {
	case m.Path != "":
		return m.Path, "path"
	case m.Header != "":
		return m.Header, "header"
	case m.Cookie != "":
		return m.Cookie, "cookie"
	case m.Query != "":
		return m.Query, "query"
	case m.Form != "":
		return m.Form, "form"
	}
	return "", ""
}

// fieldCache Q 类型 -> 字段元数据缓存。
// 跨适配器共享：解析结果只依赖 Go 类型与标签约定，与框架无关。
var fieldCache sync.Map

// ParseFields 反射解析结构体字段元数据（内嵌结构体递归展平）。
// 挂载期调用一次即缓存；各框架适配器共享同一份解析结果。
func ParseFields(t reflect.Type) []FieldMeta {
	if v, ok := fieldCache.Load(t); ok {
		return v.([]FieldMeta)
	}
	var out []FieldMeta
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				out = append(out, FieldMeta{Children: ParseFields(ft)})
			}
			continue
		}
		if !f.IsExported() {
			continue
		}
		var m FieldMeta
		m.Index = i
		m.Kind = f.Type.Kind()
		m.Path, _ = TagValue(f, "path")
		m.Query, _ = TagValue(f, "query")
		m.Form, _ = TagValue(f, "form")
		m.Header, _ = TagValue(f, "header")
		m.Cookie, _ = TagValue(f, "cookie")
		m.Def = f.Tag.Get("default")
		out = append(out, m)
	}
	fieldCache.Store(t, out)
	return out
}
