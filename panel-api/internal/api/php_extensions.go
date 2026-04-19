package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/phpext"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
)

// ExtensionApplyRequest binds the POST body for the apply endpoint. The
// allowed action set matches the agent's php.ext.apply contract and is
// locked in panel-api/internal/agent/php_ext_contract_test.go.
type ExtensionApplyRequest struct {
	Action string `json:"action" binding:"required,oneof=install remove enable disable"`
}

// RegisterPHPExtensionAdminRoutes mounts admin-only PHP extension management
// routes. The group is expected to already carry RequireAuth + RequireAdmin.
//
// GET  /php/versions/:version/extensions
// POST /php/versions/:version/extensions/:ext/apply
//
// The handlers validate version + ext via the phpext allowlist BEFORE calling
// the agent — unknown values never reach the socket. Errors from the agent
// are translated through translateAgentError (see health_agent.go) which
// maps CodeFailedPrecondition to 409 Conflict.
func RegisterPHPExtensionAdminRoutes(rg *gin.RouterGroup, cli agent.AgentInterface) {
	php := rg.Group("/php")

	php.GET("/versions/:version/extensions", func(c *gin.Context) {
		version := c.Param("version")
		if !phpext.ValidVersion(version) {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_argument", "detail": "invalid version format"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), phpVersionCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "php.ext.list", map[string]string{"version": version})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	php.POST("/versions/:version/extensions/:ext/apply", func(c *gin.Context) {
		version := c.Param("version")
		ext := c.Param("ext")
		if !phpext.ValidVersion(version) {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_argument", "detail": "invalid version format"})
			return
		}
		if _, ok := phpext.Lookup(ext); !ok {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "not_found", "detail": "unknown extension"})
			return
		}
		var req ExtensionApplyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_argument", "detail": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), adminActionTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "php.ext.apply", map[string]string{
			"version": version,
			"ext":     ext,
			"action":  req.Action,
		})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})
}
