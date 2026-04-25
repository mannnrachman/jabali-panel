package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// filesUploadSizeLimit caps request body size using http.MaxBytesReader.
// Applied to /files/upload. The actual error surfaces inside the upload
// handler when multipart parsing fails.
//
// maxBytesFn is evaluated per-request so admin changes to the
// server_settings.upload_max_size_mb knob take effect on the very next
// upload without an app restart.
func filesUploadSizeLimit(maxBytesFn func(*gin.Context) int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytesFn(c))
		c.Next()
	}
}
