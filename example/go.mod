module github.com/EdSan845D/oapi-hinge/example

go 1.26.5

require (
	github.com/EdSan845D/oapi-hinge/contract v0.0.0
	github.com/EdSan845D/oapi-hinge/openapi v0.0.0
	github.com/EdSan845D/oapi-hinge/servergin v0.0.0
	github.com/bdpiprava/scalar-go v0.13.0
	github.com/getkin/kin-openapi v0.142.0
	github.com/gin-gonic/gin v1.10.0
)
replace (
	github.com/EdSan845D/oapi-hinge/contract => ../contract
	github.com/EdSan845D/oapi-hinge/openapi => ../openapi
	github.com/EdSan845D/oapi-hinge/servergin => ../servergin
)
