package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
)

// systemCallTimeout bounds agent calls for system endpoints. Generous
// because service.list probes multiple units sequentially.
const phpVersionCallTimeout = 10 * time.Second

// adminActionTimeout bounds admin-only install/reload operations.
const adminActionTimeout = 5 * time.Minute

// reloadTimeout bounds the reload operation specifically.
const reloadTimeout = 30 * time.Second

// RegisterPHPVersionRoutes mounts GET /php/versions under the given router group.
// The group is expected to already carry RequireAuth middleware. This endpoint
// is available to all authenticated users (admin and non-admin) since they both
// need to select PHP versions for their domains.
func RegisterPHPVersionRoutes(rg *gin.RouterGroup, cli agent.AgentInterface) {
	php := rg.Group("/php")

	php.GET("/versions", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), phpVersionCallTimeout)
		defer cancel()

		raw, err := cli.Call(ctx, "php.version.list", nil)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})
}

// RegisterPHPVersionAdminRoutes mounts admin-only PHP version management routes.
// The group is expected to carry RequireAuth and RequireAdmin middleware.
func RegisterPHPVersionAdminRoutes(rg *gin.RouterGroup, cli agent.AgentInterface) {
	php := rg.Group("/php")

	// GET /php/versions/status — return full version matrix
	php.GET("/versions/status", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), phpVersionCallTimeout)
		defer cancel()

		raw, err := cli.Call(ctx, "php.version.status", nil)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	// POST /php/versions/:version/install — install a PHP version
	php.POST("/versions/:version/install", func(c *gin.Context) {
		version := c.Param("version")

		// Validate version is supported
		if !isVersionSupported(version) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "unsupported version: " + version,
			})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), adminActionTimeout)
		defer cancel()

		params, _ := json.Marshal(map[string]string{"version": version})
		raw, err := cli.Call(ctx, "php.version.install", params)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	// POST /php/versions/:version/reload — reload a PHP-FPM service
	php.POST("/versions/:version/reload", func(c *gin.Context) {
		version := c.Param("version")

		// Validate version is supported
		if !isVersionSupported(version) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "unsupported version: " + version,
			})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), reloadTimeout)
		defer cancel()

		params, _ := json.Marshal(map[string]string{"version": version})
		raw, err := cli.Call(ctx, "php.version.reload", params)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})
}

// isVersionSupported checks if a version is in the supported list.
// Must match the list in panel-agent/internal/commands/php_version_status.go
var supportedPHPVersions = []string{"7.4", "8.0", "8.1", "8.2", "8.3", "8.4", "8.5"}

func isVersionSupported(version string) bool {
	for _, v := range supportedPHPVersions {
		if v == version {
			return true
		}
	}
	return false
}
