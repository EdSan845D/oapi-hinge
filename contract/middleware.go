package contract

import (
	"reflect"
	"runtime"
	"strings"
)

// FuncName 反射提取函数名（稳定标识，供文档钩子匹配）。
// 返回「包.函数」形态（如 middleware.Auth），比裸函数名更不易跨包撞名。
func FuncName(fn any) string {
	if fn == nil {
		return ""
	}
	v := reflect.ValueOf(fn)
	name := runtime.FuncForPC(v.Pointer()).Name()
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(name, "-fm")
	return name
}