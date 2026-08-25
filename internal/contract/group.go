package contract

// Group 路由分组（树形）：对应 gin.RouterGroup / echo.Group 的嵌套模型。
//   - Prefix 为空表示根组
//   - 组内 Route.Path 为相对组前缀的路径（列表路由用 "" 表示组根）
//   - Middlewares 沿树向子组继承（运行时与文档生成行为一致）
//   - 文档侧 Tags 合并：组 Tags + 路由 Tags
type Group struct {
	Prefix      string
	Description string
	Tags        []string
	Middlewares []any // 框架中间件函数；名字由反射派生（见 FuncName），文档侧按名匹配钩子
	Routes      []Route
	Children    []*Group
}