package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// RequireContentType rejects POST/PATCH/PUT requests with a missing or
// unrecognised Content-Type. Accepted types are application/json and
// multipart/form-data (file-upload endpoints). GET, DELETE, and other
// body-less methods are passed through unchanged.
func RequireContentType() gin.HandlerFunc {
	return func(c *gin.Context) {
		m := c.Request.Method
		if m != http.MethodPost && m != http.MethodPatch && m != http.MethodPut {
			c.Next()
			return
		}
		ct := c.GetHeader("Content-Type")
		if !strings.HasPrefix(ct, "application/json") && !strings.HasPrefix(ct, "multipart/form-data") {
			c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, gin.H{
				"error": "unsupported_content_type",
			})
			return
		}
		c.Next()
	}
}
