// github.com/EdSan845D/oapi-hinge/serverecho — echo 适配器子模块。
//
// 框架适配器与内核分离：只 import 内核（github.com/EdSan845D/oapi-hinge/hinge，
// 位于根模块）的项目不引入 echo；按需 import 本模块才携带 echo 依赖。
// 发版：随仓库主版本打子模块 tag（如 serverecho/v0.2.0）。
module github.com/EdSan845D/oapi-hinge/serverecho

go 1.25

require (
	github.com/EdSan845D/oapi-hinge v0.2.1
	github.com/labstack/echo/v4 v4.15.4
)

require (
	github.com/labstack/gommon v0.5.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)

// 仓内开发：仓库根 go.work 把内核解析到父目录（本模块无需 replace）；
// 作为依赖被引用时以 require 的 tag 版本为准。
