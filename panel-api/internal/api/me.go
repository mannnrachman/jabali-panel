package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// MeHandlerConfig carries the repos the /me/* sub-routes need. The bare
// /me endpoint still uses only JWT claims, so passing empty repos is
// fine for tests that only exercise it.
type MeHandlerConfig struct {
	Users          repository.UserRepository
	ServerSettings repository.ServerSettingsRepository
}

// RegisterMeRoutes wires GET /api/v1/me and GET /api/v1/me/ssh-connection.
// The group passed here must already have RequireAuth applied.
func RegisterMeRoutes(g *gin.RouterGroup, cfg MeHandlerConfig) {
	g.GET("/me", meHandler)
	if cfg.Users != nil && cfg.ServerSettings != nil {
		h := &meExtHandler{cfg: cfg}
		g.GET("/me/ssh-connection", h.sshConnection)
		// M37 Phase 4: server capability flags any signed-in user
		// (admin OR tenant) needs to render the right UI. Currently
		// only postgres_enabled — add fields here when more
		// engine-/feature-gated UI lands.
		g.GET("/me/server-capabilities", h.serverCapabilities)
	}
}

// serverCapabilities returns the operator-controlled flags the SPA
// reads to decide whether to expose engine choices, app types, etc.
// Read-only mirror of the relevant server_settings fields, scoped to
// what's safe to share with non-admin tenants.
func (h *meExtHandler) serverCapabilities(c *gin.Context) {
	ctx := c.Request.Context()
	settings, err := h.cfg.ServerSettings.Get(ctx)
	if errors.Is(err, repository.ErrNotFound) {
		// Pre-seed install — every flag defaults to false.
		c.JSON(http.StatusOK, gin.H{"postgres_enabled": false})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"postgres_enabled": settings.PostgresEnabled,
	})
}

func meHandler(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		// Belt and braces — RequireAuth should have aborted already.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":       claims.UserID,
		"email":    claims.Email,
		"is_admin": claims.IsAdmin,
	})
}

type meExtHandler struct{ cfg MeHandlerConfig }

// sshConnection returns everything the SSH Keys page needs to render the
// Connection Details card: server hostname + SSH port (from admin
// settings) and the caller's Linux username. The `command` field is the
// ready-to-copy ssh one-liner; callers don't have to know whether a
// custom port means `-p` or not.
func (h *meExtHandler) sshConnection(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	ctx := c.Request.Context()

	user, err := h.cfg.Users.FindByID(ctx, claims.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "user_not_found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if user.Username == nil || *user.Username == "" {
		// Admins have no Linux account — they don't SFTP in.
		c.JSON(http.StatusConflict, gin.H{
			"error":  "no_linux_account",
			"detail": "this account has no Linux username, SSH access is not applicable",
		})
		return
	}

	settings, err := h.cfg.ServerSettings.Get(ctx)
	if errors.Is(err, repository.ErrNotFound) {
		settings = nil
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	host := ""
	port := uint16(22)
	if settings != nil {
		host = settings.Hostname
		if settings.SSHPort != 0 {
			port = settings.SSHPort
		}
	}
	if host == "" {
		// Fall back to the request's Host header so the page can still
		// render something useful when the admin hasn't filled identity
		// in yet. Strips any ":port" suffix since the browser attaches
		// the panel's own port, not the ssh one.
		host = c.Request.Host
		for i := range host {
			if host[i] == ':' {
				host = host[:i]
				break
			}
		}
	}

	cmd := fmt.Sprintf("ssh %s@%s", *user.Username, host)
	if port != 22 {
		cmd = fmt.Sprintf("ssh -p %d %s@%s", port, *user.Username, host)
	}

	c.JSON(http.StatusOK, gin.H{
		"host":     host,
		"port":     port,
		"username": *user.Username,
		"command":  cmd,
	})
}
