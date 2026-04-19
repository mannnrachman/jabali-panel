package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// filesUploadSizeLimit caps request body size using http.MaxBytesReader.
// Applied to /files/upload. The actual error surfaces inside the upload
// handler when multipart parsing fails.
func filesUploadSizeLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}
