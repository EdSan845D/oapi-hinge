package openapi

import (
	"github.com/EdSan845D/oapi-hinge/contract"

	"github.com/getkin/kin-openapi/openapi3"
)

// DocHook 中间件文档钩子：生成 operation 时被调用，可修改任意字段
// （security、header 参数、响应码等）。未注册钩子的中间件不进文档。
type DocHook = func(op *openapi3.Operation)

// 中间件文档钩子注册表：键为反射派生的函数名（contract.FuncName）
var hooks = map[string]DocHook{}

// RegisterMiddlewareDoc 把中间件函数与文档钩子绑定（可选择性注册）。
// fn 传业务中间件函数引用（如 middleware.Auth），内部反射取名字做键，
// 调用方不需要手写名字字符串。
func RegisterMiddlewareDoc(fn any, h DocHook) {
	name := contract.FuncName(fn)
	if name == "" {
		panic("RegisterMiddlewareDoc: invalid middleware function")
	}
	if _, dup := hooks[name]; dup {
		panic("middleware doc hook already registered: " + name)
	}
	hooks[name] = h
}

// applyHooks 按中间件函数名匹配并应用文档钩子
func applyHooks(op *openapi3.Operation, mws []any) {
	for _, mw := range mws {
		if h, ok := hooks[contract.FuncName(mw)]; ok {
			h(op)
		}
	}
}