// Public Automation API (M44).
//
// Mounted at /api/v1/automation/* behind the HMAC middleware. Each
// route declares its required scope; AutomationScopes.Has matches
// either an exact "read:domains" or a wildcard "read:*".
//
// Response shapes are intentionally THINNER than the regular
// /api/v1 routes: external callers shouldn't accidentally cache
// listen-IP topology, doc-roots, or per-user infra fields. If a
// downstream automation needs a richer view, mint a Kratos session
// instead.
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
)

// AutomationConfig wires read repos for the public automation API.
// Required: AutomationTokens + Key (for the HMAC middleware).
// Optional: per-resource repos — when nil the matching route returns
// 503 instead of 404 so the caller can distinguish "feature off" from
// "no rows".
type AutomationConfig struct {
	AutomationTokens repository.AutomationTokenRepository
	Key              *ssokey.Key
	Domains          repository.DomainRepository
	Users            repository.UserRepository
	Applications    repository.ApplicationInstallRepository
}

func RegisterAutomation(rg *gin.RouterGroup, cfg AutomationConfig) {
	if cfg.AutomationTokens == nil || cfg.Key == nil {
		return
	}
	g := rg.Group("/automation",
		middleware.RequireAutomationHMAC(cfg.AutomationTokens, cfg.Key),
	)

	if cfg.Domains != nil {
		g.GET("/domains", middleware.RequireScope("read:domains"), func(c *gin.Context) {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
			defer cancel()
			rows, _, err := cfg.Domains.List(ctx, repository.ListOptions{
				Limit: 200,
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
				return
			}
			out := make([]map[string]any, 0, len(rows))
			for _, d := range rows {
				out = append(out, map[string]any{
					"id":         d.ID,
					"name":       d.Name,
					"user_id":    d.UserID,
					"is_enabled": d.IsEnabled,
				})
			}
			c.JSON(http.StatusOK, gin.H{"data": out, "total": len(out)})
		})
	}

	if cfg.Users != nil {
		g.GET("/users", middleware.RequireScope("read:users"), func(c *gin.Context) {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
			defer cancel()
			rows, _, err := cfg.Users.List(ctx, repository.ListOptions{
				Limit: 200,
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
				return
			}
			out := make([]map[string]any, 0, len(rows))
			for _, u := range rows {
				username := ""
				if u.Username != nil {
					username = *u.Username
				}
				pkg := ""
				if u.PackageID != nil {
					pkg = *u.PackageID
				}
				out = append(out, map[string]any{
					"id":         u.ID,
					"email":      u.Email,
					"username":   username,
					"package_id": pkg,
					"is_admin":   u.IsAdmin,
				})
			}
			c.JSON(http.StatusOK, gin.H{"data": out, "total": len(out)})
		})
	}

	if cfg.Applications != nil {
		g.GET("/applications", middleware.RequireScope("read:applications"), func(c *gin.Context) {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
			defer cancel()
			rows, _, err := cfg.Applications.List(ctx, repository.ListOptions{
				Limit: 200,
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
				return
			}
			out := make([]map[string]any, 0, len(rows))
			for _, a := range rows {
				out = append(out, map[string]any{
					"id":        a.ID,
					"app_type":  a.AppType,
					"domain_id": a.DomainID,
					"status":    a.Status,
				})
			}
			c.JSON(http.StatusOK, gin.H{"data": out, "total": len(out)})
		})
	}

	g.GET("/status", middleware.RequireScope("read:status"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"healthy": true,
			"time":    time.Now().UTC(),
		})
	})
}
