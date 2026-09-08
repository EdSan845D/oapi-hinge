//go:build openapi

package openapi

import (
	"github.com/EdSan845D/oapi-hinge/hinge"

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

// OptionWithSecurity 设置安全方案（如 BearerAuth）。
// 端点 Middleware 名命中 scheme 名 → 推导 security + 401
// （oapi:auth / oapi:limit 为 oapi:middleware 的别名，值统一进 Middleware 名单）。
func OptionWithSecurity(schemes openapi3.SecuritySchemes) Option {
	return func(doc *openapi3.T) {
		doc.Components.SecuritySchemes = schemes
		securityNames = securityNames[:0]
		for name := range schemes {
			securityNames = append(securityNames, name)
		}
	}
}

// OptionWithEnvelope 注入运行时实际使用的默认响应壳实例。
// 生成期调用其 Success/Failure 反射推导成功/失败响应 schema——
// 文档形态与运行时同构，消灭手工配对。
// 优先级：Endpoint.Envelope 命名壳（oapi:envelope 注解，配对
// OptionWithEnvelopeSchemas）> 本 Option > 手写壳 schema > 默认壳推导。
func OptionWithEnvelope(env hinge.Envelope) Option {
	return func(doc *openapi3.T) {
		if env != nil {
			envelopeInstance = env
		}
	}
}

// OptionWithSourceComments 开启「注释即文档」：解析主模块源码注释生成描述。
//   - 字段上方注释 → query/header/path 参数与 body 字段的 description
//   - 结构体上方注释 → components 组件 description
//
// 只解析主模块（go.mod 向上定位）的包；description 标签优先于注释；
// 配合 RegisterCommentParser 可自定义解析语义（如从注释提取 example）。
// v0.2 端点级 Summary/Description 来自 oapi:* 注解，不再依赖 handler 注释兜底。
func OptionWithSourceComments() Option {
	return func(doc *openapi3.T) {
		sourceComments = true
	}
}

// EnvelopeSchema 响应壳 schema 包装函数：输入业务数据 schema，输出壳 schema。
// 手写壳 schema：仅当无法从壳实例推导（如 map 形态壳需要精确 key 文档）时使用；
// 常规场景用 OptionWithEnvelope 自动推导。
type EnvelopeSchema func(data *openapi3.SchemaRef) *openapi3.SchemaRef

// defaultEnvelopeSchema {code, data, msg} 壳 schema（DefaultEnvelope 形态；
// 显式 OptionWithEnvelopeSchema 时使用；内核默认裸壳无需手写）
func defaultEnvelopeSchema(data *openapi3.SchemaRef) *openapi3.SchemaRef {
	env := openapi3.NewObjectSchema()
	env.Properties = openapi3.Schemas{
		"code": {Value: openapi3.NewIntegerSchema()},
		"data": data,
		"msg":  {Value: openapi3.NewStringSchema()},
	}
	return &openapi3.SchemaRef{Value: env}
}

// OptionWithEnvelopeSchema 手写默认壳 schema：自定义响应壳在 OpenAPI 文档中的 schema。
// 传入 nil 恢复默认（裸壳推导）。
// 注意：若配置了 OptionWithEnvelope，壳实例推导优先于本选项。
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

// OptionWithEnvelopeSchemas 注册命名壳的文档 schema（oapi:envelope <name> 注解引用）。
// 端点 Endpoint.Envelope 命中注册表时使用对应壳 schema；
// 未命中输出警告并退回默认壳推导。运行时侧需以同名 hinge.RegisterEnvelope 配对。
func OptionWithEnvelopeSchemas(schemas map[string]EnvelopeSchema) Option {
	return func(doc *openapi3.T) {
		for name, fn := range schemas {
			if name == "" || fn == nil {
				continue
			}
			if envelopeSchemas == nil {
				envelopeSchemas = map[string]EnvelopeSchema{}
			}
			envelopeSchemas[name] = fn
		}
	}
}
