package openapi

import (
	"reflect"
	"runtime"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// 中间件文档钩子：把中间件函数引用与 operation 改写函数绑定，
// 生成 OpenAPI 文档时对引用了该中间件的端点调用钩子（security、参数、
// 响应码、扩展等均可改写），实现「中间件 → 接口文档语义」的自定义。
//
// 配对机制：hinge gen 把每个端点的中间件引用发射为 Endpoint.MWRefs——
// 源码引用为 "import路径.FuncName" 全限定形态（与 runtime.FuncForPC 派生的
// 函数名一致），内核拦截器注册名为原名。本文件的注册表按同一键空间配对。
//
// 进程级注册（同 hinge.RegisterInterceptor 语义）：Generate 不重置；
// 同名重复注册 panic（装配期冲突尽早暴露）。

// DocHook 中间件文档钩子：生成 operation 时被调用，可修改任意字段
// （security、header 参数、响应码、扩展等）。
type DocHook = func(op *openapi3.Operation)

// mwHooks 中间件文档钩子注册表。
var mwHooks = map[string]DocHook{}

// RegisterMiddlewareDoc 注册中间件文档钩子。fn 传中间件函数引用，
// 反射取全限定名做键（与生成侧 MWRefs 对齐，调用方无需手写名字字符串）；
// 引用了该中间件的端点在生成 operation 时调用 h。同名重复注册 panic。
//
// 例：
//
//	openapi.RegisterMiddlewareDoc(middleware.Auth, func(op *openapi3.Operation) {
//		op.Security = &openapi3.SecurityRequirements{{"BearerAuth": {}}}
//		op.Responses.Set("401", ...)
//	})
func RegisterMiddlewareDoc(fn any, h DocHook) {
	name := funcRefName(fn)
	if name == "" {
		panic("openapi.RegisterMiddlewareDoc: invalid middleware function（需为具名包级函数）")
	}
	if _, dup := mwHooks[name]; dup {
		panic("openapi: middleware doc hook already registered: " + name)
	}
	mwHooks[name] = h
}

// funcRefName 函数引用的全限定名（runtime 形态，去方法值后缀）；
// 非具名包级函数（闭包/方法值）返回 ""（无法生成稳定引用）。
func funcRefName(fn any) string {
	rv := reflect.ValueOf(fn)
	if rv.Kind() != reflect.Func {
		return ""
	}
	full := runtime.FuncForPC(rv.Pointer()).Name()
	full = strings.TrimSuffix(full, "-fm")
	if full == "" || strings.Contains(full, ".(") || strings.Contains(full, "..") || !strings.Contains(full, "/") {
		return ""
	}
	return full
}

// applyMiddlewareHooks 中间件文档钩子配对：按 MWRefs 全限定引用应用钩子
// （同一中间件在类型级与方法级重复引用时去重后的名单，钩子仅生效一次）。
func applyMiddlewareHooks(op *openapi3.Operation, refs []string) {
	for _, ref := range refs {
		if h, ok := mwHooks[ref]; ok {
			h(op)
		}
	}
}
