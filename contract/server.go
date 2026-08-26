package contract

// Server 运行时适配器接口规范：各框架适配器（servergin / serverecho）
// 提供具体实现，业务装配层面向该接口编程时可互换适配器。
type Server interface {
	Mount(g any, groups []*Group)
}