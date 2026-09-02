package hinge

import (
	"encoding/json"
	"errors"
	"mime/multipart"
)

// RawBody 原始请求体：B 声明为该类型时，生成绑定器不做任何解码，
// 直接把 body 字节整包填入。用于 webhook 签名校验、自定义编码等场景。
type RawBody []byte

// FileHeader 上传文件（multipart file part）。标准库类型别名，零框架依赖。
// B 中出现该类型字段（含切片 *FileHeader / []*FileHeader）即由生成器产出
// multipart 绑定分支；字段必须带 form 标签声明 part 名（生成期校验）。
type FileHeader = multipart.FileHeader

// DecodeJSON 解码 JSON 请求体到 v（指针）。gin 默认引擎对未知字段宽松，
// 这里同样保持宽松（与 v0.1 ShouldBindJSON 行为一致）。
func DecodeJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// AsBindError 把 JSON 解码错误映射为字段级绑定错误；
// 无法定位字段时返回 nil（调用方按普通错误处理）。
func AsBindError(err error) *BindError {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		return &BindError{Fields: []BindFieldError{{
			Field: te.Field, In: "body", Msg: "类型错误，期望 " + te.Type.String(),
		}}}
	}
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return &BindError{Fields: []BindFieldError{{
			In: "body", Msg: "JSON 语法错误",
		}}}
	}
	return nil
}

// WrapBindErr 把绑定阶段的普通错误（InTransform / Validate / 解码失败等）
// 包装为不带字段明细的绑定错误：Error() 保留原始信息，经统一错误链输出。
func WrapBindErr(err error) error {
	if err == nil {
		return nil
	}
	var be *BindError
	if errors.As(err, &be) {
		return be
	}
	return &BindError{Fields: []BindFieldError{{Msg: err.Error()}}}
}
