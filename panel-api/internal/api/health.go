// Package api exposes the HTTP surface of the panel backend.
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Version is the build-time version string. Overridden at link time with -ldflags.
var Version = "dev"

// RegisterHealthRoutes wires the /health endpoint onto r.
//
// /health returns 200 + {"status":"ok","version":"<Version>"} when the
// process is running. It does not yet probe dependencies (DB, agent socket);
// that is added in later phases as those subsystems come online.
func RegisterHealthRoutes(r *gin.Engine) {
	r.GET("/health", healthHandler)
}

func healthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"version": Version,
	})
}
