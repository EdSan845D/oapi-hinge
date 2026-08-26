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
