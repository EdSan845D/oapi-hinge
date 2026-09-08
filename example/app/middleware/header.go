package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// oapi:middleware
func ParseHeaderWithInfo(c *gin.Context) {
	sid := c.GetHeader("X-SessionId")
	if sid == "" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "must need session id header"})
		return
	}
	c.Next()
}
