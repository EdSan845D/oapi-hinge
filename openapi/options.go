//go:build openapi

package openapi

import (
	"github.com/EdSan845D/oapi-hinge/contract/response"
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

// OptionWithEnvelope 注入运行时实际使用的响应壳实例。
// 生成期调用其 Success/Failure 反射推导成功/失败响应 schema——
// 文档形态与运行时同构，消灭手工配对。壳实例建议放业务共享包
// （如 routes.APIEnvelope），main.go 与 main_doc.go 引用同一份。
// 优先级：RouteMeta.Envelope（路由级）> 本 Option > 手写逃生舱 > 默认壳推导。
func OptionWithEnvelope(env response.Envelope) Option {
	return func(doc *openapi3.T) {
		if env != nil {
			envelopeInstance = env
		}
	}
}

// OptionWithSourceComments 开启「注释即文档」：解析主模块源码注释生成描述。
//   - 字段上方注释 → query/header/path 参数与 body 字段的 description
//   - 结构体上方注释 → components 组件 description
//   - handler 函数上方注释 → operation Summary/Description 兜底（RouteMeta 未写时）
//
// 只解析主模块（go.mod 向上定位）的包；description 标签优先于注释；
// 配合 RegisterCommentParser 可自定义解析语义（如从注释提取 example）。
func OptionWithSourceComments() Option {
	return func(doc *openapi3.T) {
		sourceComments = true
	}
}

// EnvelopeSchema 响应壳 schema 包装函数：输入业务数据 schema，输出壳 schema。
// 手写逃生舱：仅当无法从壳实例推导（如 map 形态壳需要精确 key 文档）时使用；
// 常规场景用 OptionWithEnvelope 自动推导。
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

// OptionWithEnvelopeSchema 手写逃生舱：自定义响应壳在 OpenAPI 文档中的 schema。
// 传入 nil 恢复默认 {code, data, msg}。
// 注意：若配置了 OptionWithEnvelope 或路由级 Envelope，实例推导优先于本逃生舱。
func OptionWithEnvelopeSchema(fn EnvelopeSchema) Option {
	return func(doc *openapi3.T) {
		if fn != nil {
			envelopeSchema = fn
			envelopeSchemaCustom = true
		} else {
			envelopeSchema = defaultEnvelopeSchema
			envelopeSchemaCustom = false
		}
	}
}
