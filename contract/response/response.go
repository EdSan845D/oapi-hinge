// Package response 统一响应数据壳与业务错误码约定（零框架依赖）。
// gin 专属的响应写出函数位于 servergin 子模块（servergin 包）。
package response

// Response 统一响应壳：所有业务接口返回 {code, data, msg}
type Response[T any] struct {
	Code int    `json:"code"`
	Data T      `json:"data"`
	Msg  string `json:"msg"`
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