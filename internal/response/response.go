// Package response 统一响应壳与业务错误码约定。
package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

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

// OkWithData 成功 + 数据
func OkWithData(c *gin.Context, data any) {
	c.PureJSON(http.StatusOK, Response[any]{CodeOK, data, "操作成功"})
}

// Ok 成功（data 为 null）
func Ok(c *gin.Context) {
	OkWithData(c, nil)
}

// FailWithMessage 业务错误：HTTP 200 + code=7 + msg
func FailWithMessage(c *gin.Context, msg string) {
	c.PureJSON(http.StatusOK, Response[any]{CodeError, nil, msg})
}

// FailWithCode 业务错误：自定义 code
func FailWithCode(c *gin.Context, code int, msg string) {
	c.PureJSON(http.StatusOK, Response[any]{code, nil, msg})
}