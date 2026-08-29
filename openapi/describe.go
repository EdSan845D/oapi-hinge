package openapi

import (
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/EdSan845D/oapi-hinge/contract"

	"github.com/getkin/kin-openapi/openapi3"
)

// ============ 第二层文档注册机制 ============
//
// 纯文档增强的统一注册入口，与 RegisterMiddlewareDoc 同机制：
// 按 handler 函数引用注册（contract.FuncName 反射取「包.函数」做 key）。
// 所有注册只应出现在 main_doc.go（-tags openapi 构建），release 二进制零内容。

// RouteDoc 路由级纯文档增强。
type RouteDoc struct {
	// OperationID 覆盖。空 → 裸函数名；裸名跨路由重复时生成警告提示注册
	OperationID string

	// Errors 声明该接口可能返回的错误响应（4xx/5xx）。
	// 响应体默认由路由实际生效的壳推导（OptionWithEnvelope / RouteMeta.Envelope / 默认壳），
	// 保证文档与运行时形态一致；Schema 可整体覆盖。
	Errors []ErrorDecl

	// ResponseHeaders 声明成功响应的自定义响应头
	// （运行时经 contract.Response[R].Headers 动态发出，静态推不出，只能声明）
	ResponseHeaders []HeaderDecl

	// Hide 从文档中剔除该接口（运行时照常服务）——内网接口的轻量方案
	Hide bool

	// Hook 兜底逃生舱：拿到最终 operation 任意改写。
	// 应用顺序：中间件文档钩子先、本钩子最后。
	Hook DocHook
}

// ErrorDecl 错误响应声明。
type ErrorDecl struct {
	// Status HTTP 状态码（必填，<=0 时 DescribeRoute panic）
	Status int
	// Code 业务 code；0 → 跟随状态码（与运行时 resolveError 约定一致：
	// HTTP 200 → code=7，非 200 → code=status）
	Code int
	// Description 响应描述（如 "用户不存在"），同时作为响应体 msg 的示例值
	Description string
	// Schema 逃生舱：整体覆盖失败响应体（如自定义错误协议）；
	// 缺省由壳推导
	Schema *openapi3.SchemaRef
}

// HeaderDecl 成功响应头声明。
type HeaderDecl struct {
	Name        string
	Description string
	Required    bool
	Schema      *openapi3.Schema // 缺省 string
}

var (
	regMu sync.Mutex

	// routeDocs 路由文档增强注册表：FuncName(handler) -> RouteDoc
	routeDocs = map[string]RouteDoc{}
	// routeDocsUsed 已被路由树消费的 key（Generate 结束时未消费的输出警告）
	routeDocsUsed = map[string]bool{}

	// binderSchemas 自定义绑定类型的文档 schema（缺省 string）
	binderSchemas = map[reflect.Type]*openapi3.Schema{}

	// manualPaths 非模板路由补录（保序）
	manualPaths []manualPath
)

type manualPath struct {
	path string
	item *openapi3.PathItem
}

// DescribeRoute 注册路由级纯文档增强。
// handlerFn 传业务 handler 函数引用（如 handlers.GetUser），内部反射取名做 key；
// 在 Generate 之前调用（通常 main_doc.go 的 init()）。
// 同 key 重复注册 panic（防复制粘贴错）；注册后未匹配到任何路由的，
// Generate 结束时输出警告（注册不会静默失效）。
func DescribeRoute(handlerFn any, doc RouteDoc) {
	key := contract.FuncName(handlerFn)
	if key == "" {
		panic("openapi.DescribeRoute: invalid handler function")
	}
	for _, ed := range doc.Errors {
		if ed.Status <= 0 || ed.Status >= 600 {
			panic("openapi.DescribeRoute(" + key + "): ErrorDecl.Status 无效: " + strconv.Itoa(ed.Status))
		}
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := routeDocs[key]; dup {
		panic("openapi.DescribeRoute: duplicate registration for " + key)
	}
	routeDocs[key] = doc
}

// routeDocFor 取路由的文档增强（标记已消费）。
func routeDocFor(handlerFn any) (RouteDoc, bool) {
	key := contract.FuncName(handlerFn)
	regMu.Lock()
	defer regMu.Unlock()
	doc, ok := routeDocs[key]
	if ok {
		routeDocsUsed[key] = true
	}
	return doc, ok
}

// RegisterParamBinderSchema 给自定义绑定类型声明文档 schema（缺省 string）。
// 例：CSV ID 列表 → array<integer>。注册放 main_doc.go，release 零影响。
func RegisterParamBinderSchema[T any](s *openapi3.Schema) {
	var zero T
	t := reflect.TypeOf(zero)
	if t == nil {
		panic("openapi.RegisterParamBinderSchema: T 不能是接口类型")
	}
	if s == nil {
		panic("openapi.RegisterParamBinderSchema: schema 不能为空")
	}
	regMu.Lock()
	defer regMu.Unlock()
	binderSchemas[t] = s
}

// binderSchemaFor 返回类型的文档 schema；未注册返回 nil（调用方回退 string）。
func binderSchemaFor(t reflect.Type) *openapi3.Schema {
	regMu.Lock()
	defer regMu.Unlock()
	return binderSchemas[t]
}

// RegisterManualPath 补录非模板路由（混合项目里绕过统一模板的老接口）。
// 与模板路由同 path+method 冲突时 panic；item 会原样并入 Paths。
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

// mergeManualPaths 把补录路由并入文档；与模板路由 method 重叠时 panic。
func mergeManualPaths(doc *openapi3.T) {
	for _, mp := range manualPaths {
		existing := doc.Paths.Value(mp.path)
		if existing == nil {
			doc.Paths.Set(mp.path, mp.item)
			continue
		}
		for method, op := range mp.item.Operations() {
			if existing.GetOperation(method) != nil {
				panic("openapi: manual path 冲突: " + method + " " + mp.path + " 已由路由注册表生成")
			}
			existing.SetOperation(method, op)
		}
		if len(existing.Parameters) == 0 && len(mp.item.Parameters) > 0 {
			existing.Parameters = mp.item.Parameters
		}
	}
}

// unmatchedRegistrations 检查未匹配到任何路由的注册（防静默失效）。
func unmatchedRegistrations() []string {
	regMu.Lock()
	defer regMu.Unlock()
	var out []string
	for key := range routeDocs {
		if !routeDocsUsed[key] {
			out = append(out, "DescribeRoute: "+key+" 未匹配到任何路由（handler 拼写错误或路由已移除？）")
		}
	}
	for key := range hooks {
		if !hookUsed[key] {
			out = append(out, "RegisterMiddlewareDoc: "+key+" 未匹配到任何路由（中间件已移除或未挂载？）")
		}
	}
	sort.Strings(out)
	return out
}

// resetUsage 仅清空消费标记（Generate 每轮开始时调用；注册表本身保留）。
func resetUsage() {
	regMu.Lock()
	defer regMu.Unlock()
	routeDocsUsed = map[string]bool{}
	hookUsed = map[string]bool{}
}

// resetRegistries 清空注册表（仅供包内测试隔离使用）。
func resetRegistries() {
	regMu.Lock()
	defer regMu.Unlock()
	routeDocs = map[string]RouteDoc{}
	routeDocsUsed = map[string]bool{}
	binderSchemas = map[reflect.Type]*openapi3.Schema{}
	manualPaths = nil
	hookUsed = map[string]bool{}
	hooks = map[string]DocHook{}
	commentParser = nil
}
