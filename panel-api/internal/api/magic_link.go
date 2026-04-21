package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/magiclink"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// MagicLinkHandlerConfig wires the dependencies the magic-link routes need.
// Domains is required because the mint endpoint embeds the install's domain
// in the URL it returns to the caller.
type MagicLinkHandlerConfig struct {
	ApplicationInstalls repository.ApplicationInstallRepository
	Domains             repository.DomainRepository
	Tokens              repository.MagicLinkTokenRepository
	Keys                *magiclink.Keys
	// SkewTolerance is the backwards clock-skew slack on token expiry,
	// per ADR-0039 §10. Zero falls back to the 10s default.
	SkewTolerance time.Duration
}

const (
	defaultSkewTolerance = 10 * time.Second
	tokenTTL             = 60 * time.Second
)

// RegisterMagicLinkRoutes mounts the M22 endpoints. The mint endpoint
// (POST /api/v1/applications/:id/magic-link) sits under the v1 group
// where Kratos session middleware already applies. The validate endpoint
// (POST /applications/:id/magic-link/validate) is mounted on the root
// engine — unauthenticated by design, called by the WordPress must-use
// plugin server-side. See ADR-0039 §6.
func RegisterMagicLinkRoutes(v1 *gin.RouterGroup, root *gin.Engine, cfg MagicLinkHandlerConfig) {
	if cfg.SkewTolerance == 0 {
		cfg.SkewTolerance = defaultSkewTolerance
	}
	h := &magicLinkHandlers{cfg: cfg}
	v1.POST("/applications/:id/magic-link", h.mint)
	root.POST("/applications/:id/magic-link/validate", h.validate)
}

type magicLinkHandlers struct {
	cfg MagicLinkHandlerConfig
}

// mintResponse is what the panel UI gets back. The URL is ready to
// open in a new tab; expires_in is for an SPA progress hint.
type mintResponse struct {
	URL       string `json:"url"`
	ExpiresIn int    `json:"expires_in"`
}

func (h *magicLinkHandlers) mint(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	installID := c.Param("id")

	var install *models.ApplicationInstall
	var err error
	if claims.IsAdmin {
		install, err = h.cfg.ApplicationInstalls.FindByID(c.Request.Context(), installID)
	} else {
		// Non-admin: scoped to the caller's own installs. Returns
		// ErrNotFound on cross-tenant attempts so we don't leak existence.
		install, err = h.cfg.ApplicationInstalls.FindByIDAndUserID(c.Request.Context(), installID, claims.UserID)
	}
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "install not found"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "lookup failed"})
		return
	}

	domain, err := h.cfg.Domains.FindByID(c.Request.Context(), install.DomainID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "domain lookup failed"})
		return
	}

	tokenID, err := magiclink.Generate(rand.Reader)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}

	expiresAt := time.Now().Add(tokenTTL)
	tokenStr := magiclink.Sign(h.cfg.Keys.Signing(), tokenID, install.ID, expiresAt)
	hashSum := sha256.Sum256(tokenID[:])

	row := &models.MagicLinkToken{
		ID:                   ids.NewULID(),
		ApplicationInstallID: install.ID,
		PanelUserID:          claims.UserID,
		TokenHash:            hex.EncodeToString(hashSum[:]),
		ExpiresAt:            expiresAt,
		CreatedAt:            time.Now(),
	}
	if err := h.cfg.Tokens.Create(c.Request.Context(), row); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "token persist failed"})
		return
	}

	url := "https://" + domain.Name + "/?jabali_admin_login=" + tokenStr
	c.JSON(http.StatusOK, mintResponse{URL: url, ExpiresIn: int(tokenTTL.Seconds())})
}

type validateRequest struct {
	Token string `json:"token"`
}

type validateResponse struct {
	AdminUser string `json:"admin_user"`
	ExpiresIn int    `json:"expires_in"`
}

func (h *magicLinkHandlers) validate(c *gin.Context) {
	urlInstallID := c.Param("id")

	var body validateRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil || body.Token == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "malformed request"})
		return
	}

	tokenIDBytes, ok := decodeTokenIDPart(body.Token)
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "malformed token"})
		return
	}
	hashSum := sha256.Sum256(tokenIDBytes[:])
	tokenHash := hex.EncodeToString(hashSum[:])

	row, err := h.cfg.Tokens.FindByTokenHash(c.Request.Context(), tokenHash)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "token not found"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "lookup failed"})
		return
	}

	// Cross-install replay defence (ADR-0039 §5): URL must agree with
	// the row's recorded install_id. Distinct status (400) from sig
	// mismatch (401) so audit logs differentiate the attack vectors.
	if urlInstallID != row.ApplicationInstallID {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "install mismatch"})
		return
	}

	if _, err := magiclink.Verify(h.cfg.Keys, body.Token, row.ApplicationInstallID, row.ExpiresAt); err != nil {
		switch {
		case errors.Is(err, magiclink.ErrSignatureMismatch):
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "signature mismatch"})
		case errors.Is(err, magiclink.ErrMalformed):
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "malformed token"})
		default:
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "verify failed"})
		}
		return
	}

	if row.IsExpiredAt(time.Now(), h.cfg.SkewTolerance) {
		c.AbortWithStatusJSON(http.StatusGone, gin.H{"error": "token expired"})
		return
	}

	switch err := h.cfg.Tokens.MarkUsed(c.Request.Context(), row.ID); {
	case err == nil:
		// fall through
	case errors.Is(err, repository.ErrAlreadyUsed):
		c.AbortWithStatusJSON(http.StatusGone, gin.H{"error": "token already used"})
		return
	case errors.Is(err, repository.ErrLocked):
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "concurrent validate in flight"})
		return
	case errors.Is(err, repository.ErrNotFound):
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "token vanished"})
		return
	default:
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "mark used failed"})
		return
	}

	// Look up the install row to get the admin username for the WP plugin.
	install, err := h.cfg.ApplicationInstalls.FindByID(c.Request.Context(), row.ApplicationInstallID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "install lookup failed"})
		return
	}

	remaining := int(time.Until(row.ExpiresAt).Seconds())
	if remaining < 0 {
		remaining = 0
	}
	c.JSON(http.StatusOK, validateResponse{AdminUser: install.AdminUsername, ExpiresIn: remaining})
}

// decodeTokenIDPart pulls the 16-byte token_id out of the wire token
// shape (`<base64url(token_id)>.<base64url(sig)>`). Returns false on
// any format violation; the caller maps to HTTP 400.
func decodeTokenIDPart(token string) ([16]byte, bool) {
	var zero [16]byte
	dot := strings.IndexByte(token, '.')
	if dot <= 0 {
		return zero, false
	}
	idPart := token[:dot]
	// 16 raw bytes -> 22 base64url chars without padding.
	if len(idPart) != 22 {
		return zero, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(idPart)
	if err != nil || len(decoded) != 16 {
		return zero, false
	}
	var out [16]byte
	copy(out[:], decoded)
	return out, true
}
