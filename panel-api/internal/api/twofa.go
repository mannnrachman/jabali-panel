// Package api — /api/v1/auth/2fa/* handlers for TOTP enrolment + management.
// Challenge lives in auth.go (part of the login flow); this file covers the
// CRUD-ish operations an already-authenticated user performs.
package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/twofa"
)

// TOTPHandlerConfig bundles dependencies for /api/v1/auth/2fa/*.
type TOTPHandlerConfig struct {
	Users       repository.UserRepository
	BackupCodes repository.TOTPBackupCodeRepository
	SSOKey      *ssokey.Key
}

// RegisterTOTPRoutes mounts the 2FA management endpoints under the given
// group (expected /api/v1 with RequireAuth). All routes need a real access
// token; the /auth/2fa/challenge endpoint lives under RegisterAuthRoutes
// instead because it accepts a 2fa_pending token, not a full access token.
func RegisterTOTPRoutes(g *gin.RouterGroup, cfg TOTPHandlerConfig) {
	h := &totpHandler{cfg: cfg}
	grp := g.Group("/2fa")
	grp.POST("/enroll", h.enroll)
	grp.POST("/verify", h.verify)
	grp.POST("/disable", h.disable)
	grp.POST("/regen-backup", h.regenBackup)
}

type totpHandler struct{ cfg TOTPHandlerConfig }

// ---- request/response shapes ----

type enrollResponse struct {
	Secret     string `json:"secret"`
	OtpauthURL string `json:"otpauth_url"`
}

type verifyRequest struct {
	Code string `json:"code" binding:"required,len=6"`
}

type verifyResponse struct {
	BackupCodes []string `json:"backup_codes"`
}

type disableRequest struct {
	Password string `json:"password" binding:"required"`
	Code     string `json:"code"     binding:"required"`
}

type regenBackupRequest struct {
	Code string `json:"code" binding:"required,len=6"`
}

type regenBackupResponse struct {
	BackupCodes []string `json:"backup_codes"`
}

// ---- handlers ----

// enroll generates a fresh shared secret, stores it encrypted with
// totp_enabled=false (still in the enrol-pending state), and returns the
// secret + otpauth URL to the UI. Calling enroll a second time overwrites
// any prior pending secret — useful when a user loses their QR mid-flow.
func (h *totpHandler) enroll(c *gin.Context) {
	u, ok := h.currentUser(c)
	if !ok {
		return
	}
	if u.TOTPEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "already_enabled"})
		return
	}
	en, err := twofa.NewEnrolment(u.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	sealed, err := h.cfg.SSOKey.Seal([]byte(en.Secret))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if err := h.cfg.Users.SetTOTPSecret(c.Request.Context(), u.ID, sealed); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, enrollResponse{Secret: en.Secret, OtpauthURL: en.OtpauthURL})
}

// verify takes the first code from the user's authenticator app. On success,
// it flips totp_enabled=true and atomically replaces the backup codes table
// with 10 fresh codes. The raw codes are returned ONCE; the UI must force
// the user to record them before leaving the modal.
func (h *totpHandler) verify(c *gin.Context) {
	u, ok := h.currentUser(c)
	if !ok {
		return
	}
	var req verifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	if u.TOTPSecretEncrypted == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "no_pending_enrolment"})
		return
	}
	secret, err := h.cfg.SSOKey.Open(u.TOTPSecretEncrypted)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !twofa.Verify(string(secret), req.Code) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_code"})
		return
	}
	codes, hashes, err := generateBackupCodesAndHashes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	// Best-effort atomic replacement: delete old (shouldn't exist yet on
	// first verify, but defensive for edge re-verify cases) then insert new.
	ctx := c.Request.Context()
	if err := h.cfg.BackupCodes.DeleteAllByUserID(ctx, u.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if err := h.cfg.BackupCodes.CreateBatch(ctx, backupRowsFor(u.ID, hashes)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if err := h.cfg.Users.EnableTOTP(ctx, u.ID, time.Now().UTC()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, verifyResponse{BackupCodes: codes})
}

// disable requires BOTH the user's current password AND a valid TOTP code
// — preventing a stolen session token from being enough to turn 2FA off.
func (h *totpHandler) disable(c *gin.Context) {
	u, ok := h.currentUser(c)
	if !ok {
		return
	}
	var req disableRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	if !u.TOTPEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "not_enabled"})
		return
	}
	if !auth.VerifyPassword(u.PasswordHash, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_credentials"})
		return
	}
	secret, err := h.cfg.SSOKey.Open(u.TOTPSecretEncrypted)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !twofa.Verify(string(secret), req.Code) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_code"})
		return
	}
	ctx := c.Request.Context()
	if err := h.cfg.BackupCodes.DeleteAllByUserID(ctx, u.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if err := h.cfg.Users.DisableTOTP(ctx, u.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "disabled"})
}

// regenBackup replaces the 10 backup codes with a fresh set. Requires a
// current TOTP code so a stolen session can't silently rotate the user's
// recovery material out from under them.
func (h *totpHandler) regenBackup(c *gin.Context) {
	u, ok := h.currentUser(c)
	if !ok {
		return
	}
	var req regenBackupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	if !u.TOTPEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "not_enabled"})
		return
	}
	secret, err := h.cfg.SSOKey.Open(u.TOTPSecretEncrypted)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !twofa.Verify(string(secret), req.Code) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_code"})
		return
	}
	codes, hashes, err := generateBackupCodesAndHashes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	ctx := c.Request.Context()
	if err := h.cfg.BackupCodes.DeleteAllByUserID(ctx, u.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if err := h.cfg.BackupCodes.CreateBatch(ctx, backupRowsFor(u.ID, hashes)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, regenBackupResponse{BackupCodes: codes})
}

// ---- helpers ----

// currentUser loads the authenticated user. Returns false after writing a
// 401/404 response if no user is resolvable.
func (h *totpHandler) currentUser(c *gin.Context) (*models.User, bool) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return nil, false
	}
	u, err := h.cfg.Users.FindByID(c.Request.Context(), claims.UserID)
	if err != nil || u == nil {
		if errors.Is(err, repository.ErrNotFound) || u == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		}
		return nil, false
	}
	return u, true
}

// generateBackupCodesAndHashes returns the human-readable codes alongside
// the bcrypt hashes to persist, in matching order.
func generateBackupCodesAndHashes() (codes []string, hashes []string, err error) {
	codes, err = twofa.NewBackupCodes()
	if err != nil {
		return nil, nil, err
	}
	hashes = make([]string, len(codes))
	for i, code := range codes {
		h, hErr := twofa.HashCode(code)
		if hErr != nil {
			return nil, nil, hErr
		}
		hashes[i] = h
	}
	return codes, hashes, nil
}

// backupRowsFor builds the DB rows for a fresh batch.
func backupRowsFor(userID string, hashes []string) []models.TOTPBackupCode {
	now := time.Now().UTC()
	rows := make([]models.TOTPBackupCode, len(hashes))
	for i := range hashes {
		rows[i] = models.TOTPBackupCode{
			ID:        ids.NewULID(),
			UserID:    userID,
			CodeHash:  hashes[i],
			CreatedAt: now,
		}
	}
	return rows
}
