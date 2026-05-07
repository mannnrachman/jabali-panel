package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/kratosclient"
)

// reset2FA handles POST /admin/users/:id/2fa/reset.
//
// Strips totp + lookup_secret credentials from the user's Kratos
// identity. Used when a user has lost their authenticator AND burned
// through their recovery codes. The user keeps their password; on
// next login they're at aal1 and can re-enrol from /profile.
//
// Admin-only (enforced by route registration). Returns:
//   - 200 with {"ok": true} on success (or no-op if 2FA wasn't set).
//   - 404 if the user (or their kratos identity) doesn't exist.
//   - 503 if the Kratos client isn't wired (dev binaries).
//   - 500 on Kratos admin API errors.
func (h *userHandler) reset2FA(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user id required"})
		return
	}

	user, err := h.cfg.Repo.FindByID(c.Request.Context(), userID)
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	if user.KratosIdentityID == nil || *user.KratosIdentityID == "" {
		// User predates Kratos cutover or wasn't migrated. There's
		// genuinely no second-factor state to clear — but a 200 OK
		// from a "Reset 2FA" button suggests the operator's request
		// happened when nothing was actually done. Surface 422 so
		// the UI can warn explicitly: "this user has no Kratos
		// identity; run jabali admin rebuild-kratos first".
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":  "user_has_no_kratos_identity",
			"detail": "User predates Kratos migration. Run `jabali admin rebuild-kratos` to provision an identity, then re-attempt.",
		})
		return
	}

	if h.cfg.KratosClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "kratos not available"})
		return
	}

	if err := h.cfg.KratosClient.RemoveSecondFactor(c.Request.Context(), *user.KratosIdentityID); err != nil {
		if errors.Is(err, kratosclient.ErrIdentityNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "kratos identity not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "kratos admin patch failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}
