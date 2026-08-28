package contract

import (
	"reflect"
	"sync"
)

// ParamBinder 把 query/path/header 的原始字符串解析为字段值。
// src：原始值列表（单值参数长度 1；重复参数 ?ids=1&ids=2 与逗号串由实现自行处理）。
// 返回值必须可赋给字段声明类型；错误建议使用 StatusError（如 NotFound/BadRequest），
// 会汇入统一错误链（HTTP 状态码 + 响应壳）。
//
// 注册方式（通常在 init 或装配阶段）：
//
//	contract.RegisterParamBinder(func(src []string) (IDs, error) { ... })
type ParamBinder func(src []string) (any, error)

// paramBinders 字段类型 -> 自定义绑定器（全局注册表）。
// 注册式而非接口式：业务类型（如缓存实体）零侵入，转换闭包可捕获外部依赖。
var paramBinders sync.Map // reflect.Type -> ParamBinder

// RegisterParamBinder 注册某字段类型的自定义绑定器。
// T 为字段声明类型（如 IDs、*User）；同名类型重复注册覆盖。
func RegisterParamBinder[T any](fn func(src []string) (T, error)) {
	var zero T
	t := reflect.TypeOf(zero)
	paramBinders.Store(t, ParamBinder(func(src []string) (any, error) {
		v, err := fn(src)
		if err != nil {
			return nil, err
		}
		return v, nil
	}))
}

// HasParamBinder 判断类型是否注册过自定义参数绑定器。
// openapi 生成器据此把参数 schema 标注为 string——绑定器类型的 HTTP 形态是原始字符串
// （逗号串、ID 等），而非其 Go 类型的 JSON 形态。
func HasParamBinder(t reflect.Type) bool {
	_, ok := paramBinders.Load(t)
	return ok
}

// BinderFor 返回类型的自定义绑定器；未注册返回 false（适配器内部使用）。
func BinderFor(t reflect.Type) (ParamBinder, bool) {
	v, ok := paramBinders.Load(t)
	if !ok {
		return nil, false
	}
	return v.(ParamBinder), true
}
