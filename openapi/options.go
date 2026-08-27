//go:build openapi

package openapi

import (
	"github.com/getkin/kin-openapi/openapi3"
)

// OptionWithDocInfo 设置文档 Info（标题 / 版本 / 描述）
func OptionWithDocInfo(info *openapi3.Info) Option {
	return func(doc *openapi3.T) {
		doc.Info = info
	}
}

// OptionWithServer 设置服务器列表
func OptionWithServer(servers *openapi3.Servers) Option {
	return func(doc *openapi3.T) {
		doc.Servers = *servers
	}
}

// OptionWithSecurity 设置安全方案（如 BearerAuth）
func OptionWithSecurity(schemes openapi3.SecuritySchemes) Option {
	return func(doc *openapi3.T) {
		doc.Components.SecuritySchemes = schemes
	}
}

// EnvelopeSchema 响应壳 schema 包装函数：输入业务数据 schema，输出壳 schema。
// 与运行时 server.SetEnvelope / RouteMeta.Envelope 配对使用（各自独立配置）。
type EnvelopeSchema func(data *openapi3.SchemaRef) *openapi3.SchemaRef

// defaultEnvelopeSchema 默认壳 {code, data, msg}
func defaultEnvelopeSchema(data *openapi3.SchemaRef) *openapi3.SchemaRef {
	env := openapi3.NewObjectSchema()
	env.Properties = openapi3.Schemas{
		"code": {Value: openapi3.NewIntegerSchema()},
		"data": data,
		"msg":  {Value: openapi3.NewStringSchema()},
	}
	return &openapi3.SchemaRef{Value: env}
}

// OptionWithEnvelopeSchema 自定义响应壳在 OpenAPI 文档中的 schema。
// 传入 nil 恢复默认 {code, data, msg}；默认值与运行时 DefaultEnvelope 配对。
func OptionWithEnvelopeSchema(fn EnvelopeSchema) Option {
	return func(doc *openapi3.T) {
		if fn != nil {
			envelopeSchema = fn
		} else {
			envelopeSchema = defaultEnvelopeSchema
		}
	}
}
