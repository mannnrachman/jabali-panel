// Package app wires HTTP routes, middleware and lifecycle together.
// Downstream packages (handlers, middleware, repositories) plug in here.
package app

import (
	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
)

// New returns a Gin engine wired with Phase 1 routes only.
// Subsequent phases (auth, users, etc.) extend this by adding more
// RegisterXxxRoutes calls and middleware.
func New() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.HandleMethodNotAllowed = true

	api.RegisterHealthRoutes(r)
	return r
}
