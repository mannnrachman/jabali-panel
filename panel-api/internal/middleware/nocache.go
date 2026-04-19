// Package middleware — NoCacheAPI.
//
// Mounted at the root router so every /api/v1/* response (auth endpoints
// and the RequireAuth-protected tree) is tagged Cache-Control: no-store.
// Without this, browsers (especially Firefox's Opaque-Response-Blocking
// decision cache) can replay a prior 401 from cache with a 0ms status,
// preventing the SPA from completing its auth-check cycle and leaving
// the page blank until the user clears site data.
//
// Static SPA assets (/, /assets/*, etc.) are NOT tagged here — they're
// fingerprinted by Vite and benefit from the browser cache.
package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// NoCacheAPI tags every /api/v1/* response with Cache-Control: no-store
// and Pragma: no-cache. API responses are never cacheable — they're
// user/tenant-specific, and caching rejected responses actively hurts
// the SPA's auth recovery path.
func NoCacheAPI() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/v1/") {
			c.Header("Cache-Control", "no-store")
			c.Header("Pragma", "no-cache")
		}
		c.Next()
	}
}
