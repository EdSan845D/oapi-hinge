package contract

import (
	"errors"
	"fmt"
	"mime/multipart"
	"reflect"
)

// RawBody 原始请求体：B 声明为该类型时，适配器不做任何解码，
// 直接把 body 字节整包填入。用于 webhook 签名校验、自定义编码等场景；
// Content-Type 不限。声明形态：B = RawBody（值类型）。
type RawBody []byte

// FileHeader 上传文件（multipart file part）。标准库类型别名，零框架依赖。
// B 中出现该类型字段（含切片，声明 *FileHeader / []*FileHeader）即触发
// multipart 解析；字段必须带 form 标签声明 part 名（挂载期校验）。
type FileHeader = multipart.FileHeader


var (
	fileHeaderType    = reflect.TypeOf(FileHeader{})
	fileHeaderPtrType = reflect.TypeOf((*FileHeader)(nil))
)

func isFileHeaderType(t reflect.Type) bool {
	return t == fileHeaderType || t == fileHeaderPtrType
}

func isFileHeaderField(t reflect.Type) bool {
	if isFileHeaderType(t) {
		return true
	}
	if t.Kind() == reflect.Slice {
		return isFileHeaderType(t.Elem())
	}
	return false
}

// HasFileHeader 判断 B 是否声明了上传文件字段（含内嵌结构体递归）。
// 运行时适配器据此分派 multipart 绑定，文档生成器据此推导 multipart schema。
func HasFileHeader(t reflect.Type) bool {
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
			if ft.Kind() == reflect.Struct && HasFileHeader(ft) {
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

// CheckMultipartTags 挂载期校验：FileHeader 字段必须声明 form 标签（multipart
// part 名），缺失会导致文件静默丢失——挂载期直接报错并指明字段。
func CheckMultipartTags(t reflect.Type) error {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				if err := CheckMultipartTags(ft); err != nil {
					return err
				}
			}
			continue
		}
		if isFileHeaderField(f.Type) && f.Tag.Get("form") == "" {
			return fmt.Errorf("multipart body field %s (%s) must declare form tag", f.Name, f.Type)
		}
	}
	return nil
}

// BindMultipart 把 multipart 表单填入 b（*struct 指针）：
//   - File part → FileHeader / []FileHeader 字段（form 标签声明 part 名）
//   - Value part → 其余 form 标签字段（标量/切片，语义同 query 绑定）
//   - 注册过的自定义绑定器（RegisterParamBinder）同样生效
//   - default 标签：字段缺失时填充运行时默认值
//
// 解析由适配器完成（标准库 ParseMultipartForm，gin/echo 行为一致），本函数只做填充。
func BindMultipart(form *multipart.Form, b any) error {
	rv := reflect.ValueOf(b)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return errors.New("invalid multipart target")
	}
	e := rv.Elem()
	return bindMultipartFields(form, e, ParseFields(e.Type()))
}

func bindMultipartFields(form *multipart.Form, e reflect.Value, metas []FieldMeta) error {
	for _, m := range metas {
		f := e.Field(m.Index)
		if len(m.Children) > 0 {
			sub := f
			if sub.Kind() == reflect.Pointer {
				if sub.IsNil() {
					if !sub.CanSet() {
						continue
					}
					sub.Set(reflect.New(sub.Type().Elem()))
				}
				sub = sub.Elem()
			}
			if err := bindMultipartFields(form, sub, m.Children); err != nil {
				return err
			}
			continue
		}
		name := m.Form
		if name == "" {
			continue
		}
		// File part
		if isFileHeaderField(f.Type()) {
			if fhs := form.File[name]; len(fhs) > 0 {
				switch {
				case f.Type() == fileHeaderPtrType:
					f.Set(reflect.ValueOf(fhs[0]))
				case f.Type().Kind() == reflect.Slice:
					s := reflect.MakeSlice(f.Type(), 0, len(fhs))
					for _, fh := range fhs {
						s = reflect.Append(s, reflect.ValueOf(fh))
					}
					f.Set(s)
				}
			}
			continue
		}
		// Value part：自定义绑定器优先（语义同 query 绑定）
		if binder, ok := BinderFor(f.Type()); ok {
			if len(form.Value[name]) == 0 {
				continue
			}
			v, err := binder(form.Value[name])
			if err != nil {
				return err
			}
			f.Set(reflect.ValueOf(v))
			continue
		}
		if vals := form.Value[name]; len(vals) > 0 {
			if f.Kind() == reflect.Slice {
				if err := SetSliceValue(f, vals, name); err != nil {
					return err
				}
			} else if err := SetRaw(f, vals[0], name); err != nil {
				return err
			}
		} else if m.Def != "" && f.IsZero() {
			if err := SetRaw(f, m.Def, name); err != nil {
				return err
			}
		}
	}
	return nil
}
