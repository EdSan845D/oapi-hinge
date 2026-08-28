package servergin

import (
	"net/http"

	"github.com/EdSan845D/oapi-hinge/contract/response"
	"github.com/gin-gonic/gin"
)

// CodeError 业务错误码
const CodeError = response.CodeError

// defaultEnv 包级默认壳：保持公开响应函数的存量行为（{code, data, msg}）。
// 自定义壳请通过 Server.SetEnvelope / RouteMeta.Envelope 配置；
// 这些函数是独立工具函数，不感知 Server 配置。
var defaultEnv = response.DefaultEnvelope{}

// OkWithData 成功 + 数据（默认壳 {code, data, msg}）
func OkWithData(c *gin.Context, data any) {
	OkWithDataStatus(c, http.StatusOK, data)
}

// OkWithDataStatus 成功 + 数据 + 自定义状态码（默认壳）
func OkWithDataStatus(c *gin.Context, status int, data any) {
	c.PureJSON(status, defaultEnv.Success(status, data))
}

// Ok 成功（data 为 null）
func Ok(c *gin.Context) {
	OkWithData(c, nil)
}

// FailWithMessage 业务错误：HTTP 200 + code=7 + msg（默认壳）
func FailWithMessage(c *gin.Context, msg string) {
	c.PureJSON(http.StatusOK, defaultEnv.Failure(http.StatusOK, CodeError, msg))
}

// FailWithCode 业务错误：自定义 code（默认壳）
func FailWithCode(c *gin.Context, code int, msg string) {
	c.PureJSON(http.StatusOK, defaultEnv.Failure(http.StatusOK, code, msg))
}
