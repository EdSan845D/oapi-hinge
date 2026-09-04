// Package middleware 业务中间件：与具体服务强相关，留在业务层。
// 中间件函数名由反射派生（contract.FuncName），文档侧在 main_doc.go
// 按函数引用注册文档钩子（可选择性标注，见 internal/openapi.RegisterMiddlewareDoc）。
package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// oapi:interceptor
// Auth 示例鉴权中间件：Bearer token 校验，GIN_ENV=dev 时跳过。
// 挂在 contract.Group.Middlewares 上（如 app/routes/routes.go 的 users 组）。
func Auth(c *gin.Context) {
	if os.Getenv("GIN_ENV") == "dev" {
		c.Next()
		return
	}
	token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if token == "" || token != "demo-token" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	c.Next()
}
