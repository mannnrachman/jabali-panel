// Package app wires HTTP routes, middleware and lifecycle together.
// Downstream packages (handlers, middleware, repositories) plug in here.
package app

import (
	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
)

// Deps bundles the collaborators app.NewWithDeps needs so main.go keeps its
// argument list short. Anything a handler family needs — auth service,
// repositories, agent client — plugs in here.
type Deps struct {
	Auth api.AuthService
}

// New returns a Gin engine configured with development defaults. Used by
// tests and any caller that doesn't need to override. Production callers
// should use NewWith so they can pass the loaded Config.
func New() *gin.Engine {
	return NewWith(config.Defaults())
}

// NewWith builds a Gin engine with only config-driven concerns wired up —
// no auth, no DB. Useful in tests and for servicing health/index when the
// downstream deps aren't ready yet. Production callers should use
// NewWithDeps so auth routes are mounted.
func NewWith(cfg *config.Config) *gin.Engine {
	return NewWithDeps(cfg, Deps{})
}

// NewWithDeps is the canonical constructor for the server. Handlers that
// depend on external collaborators (auth service, DB repositories) are
// mounted only when their dep is non-nil, so early-phase deployments can
// still boot with a partial Deps.
func NewWithDeps(cfg *config.Config, deps Deps) *gin.Engine {
	if cfg.Server.Env == config.EnvProduction {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.HandleMethodNotAllowed = true

	api.RegisterIndexRoutes(r)
	api.RegisterHealthRoutes(r)

	if deps.Auth != nil {
		api.RegisterAuthRoutes(r, api.AuthHandlerConfig{
			Service:            deps.Auth,
			RefreshTTL:         cfg.Auth.RefreshTTL,
			CookieSecure:       cfg.CookieSecureResolved(),
			CookieSameSiteNone: false,
		})
	}

	api.RegisterNotFoundHandlers(r)
	return r
}
