// github.com/EdSan845D/oapi-hinge/validator — 校验器集成子模块。
//
// 可选集成与内核分离：只 import 内核的项目不引入 go-playground/validator；
// 需要完整规则校验（validate:"..."）时按需 import 本模块。
// 发版：随仓库主版本打子模块 tag（如 validator/v0.2.0）。
module github.com/EdSan845D/oapi-hinge/validator

go 1.25.0

require (
	github.com/EdSan845D/oapi-hinge v0.2.2
	github.com/go-playground/validator/v10 v10.30.3
)

require (
	github.com/gabriel-vasile/mimetype v1.4.13 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)

// 仓内开发：仓库根 go.work 把内核解析到父目录（本模块无需 replace）；
// 作为依赖被引用时以 require 的 tag 版本为准。
