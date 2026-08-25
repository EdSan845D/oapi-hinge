//go:build openapi

package openapi

import (
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-fuego/fuego"
)

func OptionWithDocInfo(info *openapi3.Info) Option {
	return func(oa *fuego.OpenAPI) {
		oa.Description().Info = info
	}
}

func OptionWithServer(server *openapi3.Servers) Option {
	return func(oa *fuego.OpenAPI) {
		oa.Description().Servers = *server
	}
}

func OptionWithSecurity(security openapi3.SecuritySchemes) Option {
	return func(oa *fuego.OpenAPI) {
		oa.Description().Components.SecuritySchemes = security
	}
}
