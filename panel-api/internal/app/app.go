// Package app wires HTTP routes, middleware and lifecycle together.
// Downstream packages (handlers, middleware, repositories) plug in here.
package app

import (
	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
)

// New returns a Gin engine configured with development defaults. Used by
// tests and any caller that doesn't need to override. Production callers
// should use NewWith so they can pass the loaded Config.
func New() *gin.Engine {
	return NewWith(config.Defaults())
}

// NewWith builds an engine using the supplied Config. It flips Gin into
// release mode for production, attaches Recovery + 405 handling, and
// registers Phase-1 routes. Subsequent phases add more RegisterXxxRoutes
// calls, middleware chains, and service dependencies.
func NewWith(cfg *config.Config) *gin.Engine {
	if cfg.Server.Env == config.EnvProduction {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.HandleMethodNotAllowed = true

	api.RegisterHealthRoutes(r)
	return r
}
