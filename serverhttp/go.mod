// github.com/EdSan845D/oapi-hinge/serverhttp — 标准库 http 适配器子模块。
//
// 框架适配器与内核分离：除内核（github.com/EdSan845D/oapi-hinge/hinge，
// 位于根模块）外零第三方依赖。
// 发版：随仓库主版本打子模块 tag（如 serverhttp/v0.2.0）。
module github.com/EdSan845D/oapi-hinge/serverhttp

go 1.27

require github.com/EdSan845D/oapi-hinge v0.2.1

// 仓内开发：仓库根 go.work 把内核解析到父目录（本模块无需 replace）；
// 作为依赖被引用时以 require 的 tag 版本为准。
