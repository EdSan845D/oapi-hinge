package servergin

import (
	"net/http"

	"github.com/EdSan845D/oapi-hinge/contract/response"
	"github.com/gin-gonic/gin"
)

// gin 适配器的统一响应写出函数：{code, data, msg} 壳。
// 数据壳类型（Response[T] / Paged）在 contract/response。

// CodeError 业务错误码（与 contract/response.CodeError 一致，适配器内部使用）
const CodeError = 7

// OkWithData 成功 + 数据
func OkWithData(c *gin.Context, data any) {
	c.PureJSON(http.StatusOK, response.Response[any]{response.CodeOK, data, "操作成功"})
}

// OkWithDataStatus 成功 + 数据 + 自定义状态码（逃生舱 2：contract.Response 定制状态用）
func OkWithDataStatus(c *gin.Context, status int, data any) {
	c.PureJSON(status, response.Response[any]{response.CodeOK, data, "操作成功"})
}

// Ok 成功（data 为 null）
func Ok(c *gin.Context) {
	OkWithData(c, nil)
}

// FailWithMessage 业务错误：HTTP 200 + code=7 + msg
func FailWithMessage(c *gin.Context, msg string) {
	c.PureJSON(http.StatusOK, response.Response[any]{response.CodeError, nil, msg})
}

// FailWithCode 业务错误：自定义 code
func FailWithCode(c *gin.Context, code int, msg string) {
	c.PureJSON(http.StatusOK, response.Response[any]{code, nil, msg})
}