// Package middleware 业务中间件：与具体服务强相关，留在业务层。
// 经 Enterpoint 的 oapi:middleware 注解按源码引用挂载（hinge gen 发射为
// 组级/路由级中间件）；框架无关的横切逻辑请用 hinge.Interceptor + 注册名。
package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// oapi:interceptor
// Auth 示例鉴权中间件：Bearer token 校验，GIN_ENV=dev 时跳过。
// 经 UserEp 的 oapi:middleware 注解引用，hinge gen 发射为组级中间件。
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
