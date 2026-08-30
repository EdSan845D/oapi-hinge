// Package response 统一响应数据壳、业务错误码约定与响应壳接口（零框架依赖）。
// gin / echo 适配器的响应写出函数位于各子模块。
package response

// Response 统一响应壳：默认业务接口返回 {code, data, msg}。
// 通过 server.SetEnvelope 可替换为任意自定义壳。
type Response[T any] struct {
	Code int    `json:"code"`
	Data T      `json:"data"`
	Msg  string `json:"msg"`
	// AggregatedError 批量操作的部分失败明细（contract.AggregateError.Failed）。
	// 仅当错误链携带 AggregateError 且壳支持聚合输出时非空；omitempty 保证
	// 普通请求的响应体与旧版逐字节一致。
	AggregatedError any `json:"aggregated_error,omitempty"`
}

// Paged 分页响应
type Paged[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
}

const (
	CodeOK    = 0 // 成功
	CodeError = 7 // 业务错误（默认）
)
