//go:generate go run generate.go

package main

import (
	"github.com/EdSan845D/oapi-hinge/example/app/eps"
	"github.com/EdSan845D/oapi-hinge/example/app/middleware"
	"github.com/EdSan845D/oapi-hinge/gen"
)

func EntryPointsConfig() []gen.EntryPointConfig {
	return []gen.EntryPointConfig{
		{
			Name: "SystemEp",
			Midllwares: []any{
				middleware.Auth,
			},
			FuncDecls: map[gen.FuncId]gen.RouteMeta{
				gen.FuncIdentity(eps.SystemEp.Health): {
					Summary:     "xxxxx",
					Description: "description from EntryPointConfig.FuncDecls",
					Deprecated:  true,
				},
			},
		},
	}
}

func main() {
	dir := "../.."
	cfg := gen.Config{
		Module:  "github.com/EdSan845D/oapi-hinge",
		Scan:    []string{"example/app/eps"},
		Out:     "example/apigen",
		Targets: []string{"gin", "echo", "http"},
	}
	cfg.EntryPoints = EntryPointsConfig()
	if err := gen.Run(dir, cfg, false); err != nil {
		panic(err)
	}
}
