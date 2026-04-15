package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterIndexRoutes wires a lightweight service-info endpoint at /.
//
// Blueprint §13 defines /jabali-admin and /jabali-panel as the personas'
// entry points. Both are served by the SPA (Phase 8). Until that ships,
// hitting / returns a structured JSON payload so operators who browse the
// bare URL see something useful (version, known routes, docs link) rather
// than a stock 404.
func RegisterIndexRoutes(r *gin.Engine) {
	r.GET("/", indexHandler)
}

func indexHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"service": "jabali-panel",
		"version": Version,
		"status":  "ok",
		"endpoints": gin.H{
			"health":   "/health",
			"api":      "/api/v1/ (not yet implemented)",
			"admin_ui": "/jabali-admin/ (not yet implemented)",
			"user_ui":  "/jabali-panel/ (not yet implemented)",
			"bridge":   "/api/bridge/v1/ (not yet implemented)",
			"ws":       "/ws/ (not yet implemented)",
		},
		"docs": "https://git.linux-hosting.co.il/shukivaknin/jabali2",
	})
}

// RegisterNotFoundHandlers installs JSON handlers for unmatched routes and
// disallowed methods. Keeps API consumers on a single content-type contract
// and avoids leaking Gin's default plain-text responses.
func RegisterNotFoundHandlers(r *gin.Engine) {
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "not_found",
			"path":  c.Request.URL.Path,
		})
	})
	r.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"error":  "method_not_allowed",
			"method": c.Request.Method,
			"path":   c.Request.URL.Path,
		})
	})
}
