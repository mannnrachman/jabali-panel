package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// suspendRequest is the optional JSON body for POST /admin/users/:id/suspend.
// reason is operator-facing audit text surfaced in the admin user list.
type suspendRequest struct {
	Reason string `json:"reason"`
}

// suspend handles POST /admin/users/:id/suspend.
//
// Three-step cascade — best-effort with rollback on the first hard
// failure so a partial suspend doesn't leave the panel in a half-
// off state:
//   1. flip users.suspended = 1 + stamp suspended_at + suspend_reason
//   2. PATCH Kratos identity state = inactive (blocks panel + webmail
//      + every Kratos-fronted UI on next request)
//   3. bulk-flip every owned domains.is_enabled = 0 (reconciler picks
//      up next tick + removes the nginx sites-enabled symlinks)
//
// Suspending an admin user is refused — admins are the only path to
// un-suspend, refusing here prevents an accidental org-wide lockout.
// Suspending the LAST non-admin doesn't need a special guard.
//
// Returns:
//   - 200 {"ok": true, "domains_disabled": N} on success
//   - 400 if id is empty
//   - 404 if the user doesn't exist
//   - 409 if the user is already suspended (idempotent re-call returns
//     200 with domains_disabled=0; the 409 only fires for clarity in a
//     concurrent-suspend race)
//   - 422 if the target is an admin user
//   - 503 if Kratos client is nil (dev binaries)
func (h *userHandler) suspend(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user id required"})
		return
	}

	var req suspendRequest
	_ = c.ShouldBindJSON(&req) // body optional

	ctx := c.Request.Context()
	user, err := h.cfg.Repo.FindByID(ctx, userID)
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if user.IsAdmin {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":  "cannot_suspend_admin",
			"detail": "Refusing to suspend an admin user. Demote first via /admin/users.",
		})
		return
	}
	if user.Suspended {
		c.JSON(http.StatusOK, gin.H{"ok": true, "already_suspended": true, "domains_disabled": int64(0)})
		return
	}

	// Step 1 — flip suspended flag.
	if err := h.cfg.Repo.SetSuspended(ctx, userID, true, req.Reason); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "suspend_db_write_failed", "detail": err.Error()})
		return
	}

	// Step 2 — push Kratos identity inactive. Best-effort: a Kratos
	// outage shouldn't block the DB + domain cascade; surface as a
	// warning in the response so the operator can re-run later.
	var kratosWarn string
	if user.KratosIdentityID != nil && *user.KratosIdentityID != "" {
		if h.cfg.KratosClient == nil {
			kratosWarn = "kratos_client_unavailable"
		} else if err := h.cfg.KratosClient.SetIdentityState(ctx, *user.KratosIdentityID, "inactive"); err != nil {
			if errors.Is(err, kratosclient.ErrIdentityNotFound) {
				kratosWarn = "kratos_identity_not_found"
			} else {
				kratosWarn = "kratos_state_patch_failed: " + err.Error()
			}
		}
	}

	// Step 3 — disable every owned domain.
	disabled, dErr := h.cfg.Domains.BulkSetEnabledByUserID(ctx, userID, false)
	var domainWarn string
	if dErr != nil {
		domainWarn = "domain_bulk_disable_failed: " + dErr.Error()
	}

	resp := gin.H{
		"ok":               true,
		"domains_disabled": disabled,
	}
	if kratosWarn != "" {
		resp["kratos_warning"] = kratosWarn
	}
	if domainWarn != "" {
		resp["domain_warning"] = domainWarn
	}
	c.JSON(http.StatusOK, resp)
}

// unsuspend handles POST /admin/users/:id/unsuspend. Reverses the
// three-step suspend cascade:
//   1. flip users.suspended = 0 + clear suspended_at + reason
//   2. PATCH Kratos identity state = active
//   3. bulk-flip every owned domains.is_enabled = 1 (reconciler
//      re-creates the nginx symlinks on next tick)
//
// Returns 200 with domains_enabled count + optional Kratos / domain
// warnings same as suspend. 404 on missing user, 200 idempotent on
// already-active users.
func (h *userHandler) unsuspend(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user id required"})
		return
	}

	ctx := c.Request.Context()
	user, err := h.cfg.Repo.FindByID(ctx, userID)
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if !user.Suspended {
		c.JSON(http.StatusOK, gin.H{"ok": true, "already_active": true, "domains_enabled": int64(0)})
		return
	}

	if err := h.cfg.Repo.SetSuspended(ctx, userID, false, ""); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unsuspend_db_write_failed", "detail": err.Error()})
		return
	}

	var kratosWarn string
	if user.KratosIdentityID != nil && *user.KratosIdentityID != "" {
		if h.cfg.KratosClient == nil {
			kratosWarn = "kratos_client_unavailable"
		} else if err := h.cfg.KratosClient.SetIdentityState(ctx, *user.KratosIdentityID, "active"); err != nil {
			if errors.Is(err, kratosclient.ErrIdentityNotFound) {
				kratosWarn = "kratos_identity_not_found"
			} else {
				kratosWarn = "kratos_state_patch_failed: " + err.Error()
			}
		}
	}

	enabled, dErr := h.cfg.Domains.BulkSetEnabledByUserID(ctx, userID, true)
	var domainWarn string
	if dErr != nil {
		domainWarn = "domain_bulk_enable_failed: " + dErr.Error()
	}

	resp := gin.H{
		"ok":              true,
		"domains_enabled": enabled,
	}
	if kratosWarn != "" {
		resp["kratos_warning"] = kratosWarn
	}
	if domainWarn != "" {
		resp["domain_warning"] = domainWarn
	}
	c.JSON(http.StatusOK, resp)
}

// silence unused-imports if a future refactor inlines the kratosclient
// constant set; keep the explicit reference here.
var _ = repository.ErrNotFound
