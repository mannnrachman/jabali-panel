package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	ginctx "git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DomainPHPPoolHandlerConfig wires the domain↔pool binding routes.
//
// Agent / Users / PHPPoolIniOverrides are required so the user-driven
// version switch can fire php.pool.apply immediately, mirroring the admin
// PUT /php-pools/:id flow. Without them, a version change only updates
// the DB and waits for the next reconciler tick — and only converges if
// pool.Status was flipped to "pending" first, which the reconciler uses
// as its work filter.
type DomainPHPPoolHandlerConfig struct {
	Domains             repository.DomainRepository
	PHPPools            repository.PHPPoolRepository
	PHPPoolIniOverrides repository.PHPPoolIniOverrideRepository
	Users               repository.UserRepository
	Agent               agent.AgentInterface
}

// RegisterDomainPHPPoolRoutes adds two routes under the existing /domains
// group that bind or unbind a domain to a PHP-FPM pool. Lives in its own
// file so domains.go stays under the 800-line invariant.
//
// Routes:
//   - POST   /domains/:id/php-pool  { "pool_id": "<ulid>" }  — admin path
//   - POST   /domains/:id/php-pool  { "php_version": "8.3" } — user path
//   - DELETE /domains/:id/php-pool
//
// Both paths require the caller to own the domain (or be admin). For the
// pool_id variant, the pool must also belong to the same user that owns
// the domain — this prevents a user from pointing their domain at another
// user's pool and reading another user's docroot via PHP. The php_version
// variant looks up the domain owner's (single, per ADR-0023) pool and
// returns 409 if the requested version does not match; changing a pool's
// PHP version is an admin operation via /php-pools/:id.
func RegisterDomainPHPPoolRoutes(g *gin.RouterGroup, cfg DomainPHPPoolHandlerConfig) {
	h := &domainPHPPoolHandler{cfg: cfg}
	g.POST("/domains/:id/php-pool", h.bind)
	g.DELETE("/domains/:id/php-pool", h.unbind)
}

type domainPHPPoolHandler struct{ cfg DomainPHPPoolHandlerConfig }

// bindDomainPHPPoolRequest accepts either PoolID (admin-style, explicit
// pool selection) or PHPVersion (user-style, resolved to the owner's
// single pool). Exactly one must be non-empty.
type bindDomainPHPPoolRequest struct {
	PoolID     string `json:"pool_id"`
	PHPVersion string `json:"php_version"`
}

func (h *domainPHPPoolHandler) bind(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req bindDomainPHPPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	// Require exactly one of pool_id or php_version.
	if (req.PoolID == "") == (req.PHPVersion == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pool_id_or_php_version_required"})
		return
	}

	ctx := c.Request.Context()
	domainID := c.Param("id")

	dom, err := h.cfg.Domains.FindByID(ctx, domainID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		slog.ErrorContext(ctx, "bind php-pool: load domain", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !claims.IsAdmin && dom.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Resolve request → pool. Two paths:
	//   - pool_id: explicit selection; pool must belong to the domain owner.
	//   - php_version: look up the domain owner's single pool; require its
	//     version to match. Changing a pool's PHP version is admin-only
	//     (via PUT /php-pools/:id) per ADR-0023.
	var pool *models.PHPPool
	if req.PoolID != "" {
		pool, err = h.cfg.PHPPools.FindByID(ctx, req.PoolID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "pool_not_found"})
				return
			}
			slog.ErrorContext(ctx, "bind php-pool: load pool", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
		// Cross-user binding is refused even for admins — an admin who wants
		// to run alice's domain in bob's pool almost certainly has a bug, not
		// an intent. If the use case ever comes up, it gets its own endpoint.
		if pool.UserID != dom.UserID {
			c.JSON(http.StatusForbidden, gin.H{"error": "pool_not_owned_by_domain_user"})
			return
		}
	} else {
		pool, err = h.cfg.PHPPools.FindByUserID(ctx, dom.UserID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "pool_not_found_for_user"})
				return
			}
			slog.ErrorContext(ctx, "bind php-pool: load user pool", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
		// ADR-0023 constrains each user to exactly one pool, so the only way
		// for a user to run a different PHP version for their domain is to
		// change the version of that single pool. This endpoint owns that
		// switch: update the pool in-place AND fire php.pool.apply so the
		// per-user FPM master swaps to the new php-fpm<version> binary
		// before the user reloads info.php. Status flips to "pending" so
		// the reconciler also re-tries on the next tick if the agent call
		// here fails or times out.
		if pool.PHPVersion != req.PHPVersion {
			pool.PHPVersion = req.PHPVersion
			pool.Status = "pending"
			pool.LastError = nil
			if err := h.cfg.PHPPools.Update(ctx, pool); err != nil {
				slog.ErrorContext(ctx, "bind php-pool: update pool version", "error", err, "pool_id", pool.ID, "new_version", req.PHPVersion)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
				return
			}
			slog.InfoContext(ctx, "php_pool.version_changed", "user_id", claims.UserID, "pool_id", pool.ID, "php_version", req.PHPVersion)

			// Fire the agent apply asynchronously so the request returns
			// quickly. Skipped when the helper deps are not wired (older
			// app boot path or tests that intentionally leave Agent nil)
			// — in that case the reconciler tick converges instead.
			if h.cfg.Agent != nil && h.cfg.Users != nil && h.cfg.PHPPoolIniOverrides != nil {
				go reconcilePHPPoolViaAgent(h.cfg.Agent, h.cfg.Users, h.cfg.PHPPoolIniOverrides, h.cfg.PHPPools, pool)
			}
		}
	}

	poolID := pool.ID
	oldPoolID := dom.PHPPoolID
	// SetPHPPoolID is the dedicated bind path. Domain.Update's column
	// allowlist intentionally excludes php_pool_id so generic PATCH
	// cannot mutate the binding — bind/unbind go through this method.
	if err := h.cfg.Domains.SetPHPPoolID(ctx, dom.ID, &poolID); err != nil {
		slog.ErrorContext(ctx, "bind php-pool: update domain", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	dom.PHPPoolID = &poolID

	oldPoolIDStr := ""
	if oldPoolID != nil {
		oldPoolIDStr = *oldPoolID
	}
	slog.InfoContext(ctx, "domain_php_pool.bound", "user_id", claims.UserID, "domain_id", dom.ID, "pool_id", poolID, "old_pool_id", oldPoolIDStr, "new_pool_id", poolID)

	c.JSON(http.StatusOK, gin.H{
		"domain_id":   dom.ID,
		"php_pool_id": poolID,
	})
}

func (h *domainPHPPoolHandler) unbind(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	ctx := c.Request.Context()
	domainID := c.Param("id")

	dom, err := h.cfg.Domains.FindByID(ctx, domainID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		slog.ErrorContext(ctx, "unbind php-pool: load domain", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !claims.IsAdmin && dom.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	oldPoolID := dom.PHPPoolID
	oldPoolIDStr := ""
	if oldPoolID != nil {
		oldPoolIDStr = *oldPoolID
	}
	// Use the dedicated method for the same reason as bind — see above.
	if err := h.cfg.Domains.SetPHPPoolID(ctx, dom.ID, nil); err != nil {
		slog.ErrorContext(ctx, "unbind php-pool: update domain", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	dom.PHPPoolID = nil

	slog.InfoContext(ctx, "domain_php_pool.unbound", "user_id", claims.UserID, "domain_id", dom.ID, "old_pool_id", oldPoolIDStr, "new_pool_id", "")

	c.JSON(http.StatusOK, gin.H{
		"domain_id":   dom.ID,
		"php_pool_id": nil,
	})
}
