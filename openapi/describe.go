//go:build openapi

package openapi

import (
	"reflect"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
)

// ============ 开发期文档注册机制 ============
//
// 与路由树 / handler 引用解耦（v0.2 端点表范式）：类型级 schema 覆盖、
// 非模板路由补录、注释解析器注册。所有注册只应出现在 main_doc.go
//（-tags openapi 构建），release 二进制零内容。
//
// v0.1 的 DescribeRoute / RouteDoc / RegisterParamBinderSchema（handler 引用键的
// 纯文档增强）已随路由分组树一并移除；端点级文档语义由 Endpoint 注解承载
//（Summary/Description/Tags/Status/Auth/Limit/Timeout/Envelope），见 gen.go。

var (
	regMu sync.Mutex

	// typeSchemas 类型级文档 schema 覆盖（组件替换）
	typeSchemas     = map[reflect.Type]*openapi3.Schema{}
	typeSchemaFuncs = map[reflect.Type]func() *openapi3.Schema{}

	// manualPaths 非模板路由补录（保序）
	manualPaths []manualPath
)

type manualPath struct {
	path string
	item *openapi3.PathItem
}

// RegisterTypeSchema 注册类型级文档 schema 覆盖（组件替换）。
// 被覆盖的类型在 body/组件位置使用注册 schema（$ref 结构不变）；
// query/path 参数位置仍按 HTTP 形态（标量反射）输出。
// 同类型重复注册覆盖；请勿在生成期修改注册的 schema（需要动态构建用 RegisterTypeSchemaFunc）。
func RegisterTypeSchema[T any](s *openapi3.Schema) {
	var zero T
	t := reflect.TypeOf(zero)
	if t == nil {
		panic("openapi.RegisterTypeSchema: T 不能是接口类型")
	}
	if s == nil {
		panic("openapi.RegisterTypeSchema: schema 不能为空")
	}
	regMu.Lock()
	defer regMu.Unlock()
	typeSchemas[t] = s
}

// RegisterTypeSchemaFunc 函数式类型覆盖：每次生成调用 fn 取新实例（避免共享可变状态）。
func RegisterTypeSchemaFunc[T any](fn func() *openapi3.Schema) {
	var zero T
	t := reflect.TypeOf(zero)
	if t == nil {
		panic("openapi.RegisterTypeSchemaFunc: T 不能是接口类型")
	}
	if fn == nil {
		panic("openapi.RegisterTypeSchemaFunc: 函数不能为空")
	}
	regMu.Lock()
	defer regMu.Unlock()
	typeSchemaFuncs[t] = fn
}

// typeSchemaFor 返回类型的覆盖 schema（Func 形式每次构建新实例）；未注册返回 nil。
func typeSchemaFor(t reflect.Type) *openapi3.Schema {
	regMu.Lock()
	defer regMu.Unlock()
	if fn, ok := typeSchemaFuncs[t]; ok {
		return fn()
	}
	return typeSchemas[t]
}

// RegisterManualPath 补录非模板路由（混合项目里绕过统一模板的老接口）。
// 与端点表同 path+method 冲突时 panic；item 会原样并入 Paths。
func RegisterManualPath(path string, item *openapi3.PathItem) {
	if path == "" || !strings.HasPrefix(path, "/") {
		panic("openapi.RegisterManualPath: path 必须以 / 开头")
	}
	if item == nil {
		panic("openapi.RegisterManualPath: item 不能为空")
	}
	regMu.Lock()
	defer regMu.Unlock()
	manualPaths = append(manualPaths, manualPath{path: path, item: item})
}

// RegisterCommentParser 注册自定义字段注释解析器（全局唯一，重复注册 panic）。
// src 为注释原文；sch 为字段当前 schema 引用（$ref 时 Value 为 nil，可用 DescribeSchema 包装）。
// 仅在 OptionWithSourceComments 开启后生效；未开启时 Generate 输出警告。
func RegisterCommentParser(fn CommentParser) {
	if fn == nil {
		panic("openapi.RegisterCommentParser: 解析器不能为空")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if commentParser != nil {
		panic("openapi.RegisterCommentParser: 重复注册")
	}
	commentParser = fn
}

// mergeManualPaths 把补录路由并入文档；与端点表 method 重叠时 panic。
func mergeManualPaths(doc *openapi3.T) {
	for _, mp := range manualPaths {
		existing := doc.Paths.Value(mp.path)
		if existing == nil {
			doc.Paths.Set(mp.path, mp.item)
			continue
		}
		for method, op := range mp.item.Operations() {
			if existing.GetOperation(method) != nil {
				panic("openapi: manual path 冲突: " + method + " " + mp.path + " 已由端点表生成")
			}
			existing.SetOperation(method, op)
		}
		if len(existing.Parameters) == 0 && len(mp.item.Parameters) > 0 {
			existing.Parameters = mp.item.Parameters
		}
	}
}

// resetRegistries 清空注册表（仅供包内测试隔离使用）。
func resetRegistries() {
	regMu.Lock()
	defer regMu.Unlock()
	typeSchemas = map[reflect.Type]*openapi3.Schema{}
	typeSchemaFuncs = map[reflect.Type]func() *openapi3.Schema{}
	manualPaths = nil
	commentParser = nil
}
