package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// SSLScheduler is a minimal interface for scheduling domain reconciliation.
type SSLScheduler interface {
	Schedule(domainID string)
}

// SSLHandlerConfig bundles dependencies for SSL certificate handlers.
type SSLHandlerConfig struct {
	Domains        repository.DomainRepository
	SSLCerts       repository.SSLCertificateRepository
	ServerSettings repository.ServerSettingsRepository
	Reconciler     SSLScheduler
	Config         *config.Config
}

// sslHandler provides HTTP handlers for SSL certificate endpoints.
type sslHandler struct {
	cfg SSLHandlerConfig
}

// SSLResponse represents the SSL certificate status for a domain.
type SSLResponse struct {
	Status        string     `json:"status"`
	IssuedAt      *time.Time `json:"issued_at,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	RenewalCount  int        `json:"renewal_count"`
	LastRenewedAt *time.Time `json:"last_renewed_at,omitempty"`
	LastError     *string    `json:"last_error,omitempty"`
	Staging       bool       `json:"staging"`
	CertPath      *string    `json:"cert_path,omitempty"`
	KeyPath       *string    `json:"key_path,omitempty"`
}

// newSSLHandler creates a new SSL handler.
func newSSLHandler(cfg SSLHandlerConfig) *sslHandler {
	return &sslHandler{cfg: cfg}
}

// RegisterSSLRoutes wires SSL endpoints into the given router.
func RegisterSSLRoutes(g *gin.RouterGroup, cfg SSLHandlerConfig) {
	h := newSSLHandler(cfg)

	domains := g.Group("/domains/:id")
	{
		domains.GET("/ssl", h.getSSL)
		domains.POST("/ssl", h.enableSSL)
		domains.DELETE("/ssl", h.disableSSL)
		domains.POST("/ssl/renew", h.renewSSL)
	}

	// List endpoints
	g.GET("/admin/ssl-certificates", middleware.RequireAdmin(), h.listAllSSL)
	g.GET("/ssl-certificates", h.listUserSSL)
}

// getSSL retrieves the current SSL certificate status for a domain.
// GET /api/v1/domains/:id/ssl
func (h *sslHandler) getSSL(c *gin.Context) {
	domainID := c.Param("id")

	// Load and authorize domain
	domain := h.loadDomainOwned(c, domainID)
	if domain == nil {
		return
	}

	// Load SSL certificate (may not exist)
	cert, err := h.cfg.SSLCerts.FindByDomainID(c.Request.Context(), domainID)
	if err != nil {
		if isNotFound(err) {
			// No certificate provisioned yet
			c.JSON(http.StatusNotFound, gin.H{"error": "no_certificate"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	resp := SSLResponse{
		Status:        cert.Status,
		IssuedAt:      cert.IssuedAt,
		ExpiresAt:     cert.ExpiresAt,
		RenewalCount:  cert.RenewalCount,
		LastRenewedAt: cert.LastRenewedAt,
		LastError:     cert.LastError,
		Staging:       cert.Staging,
		CertPath:      cert.CertPath,
		KeyPath:       cert.KeyPath,
	}

	c.JSON(http.StatusOK, gin.H{"ssl": resp})
}

// enableSSL enables SSL for a domain and initiates certificate issuance.
// POST /api/v1/domains/:id/ssl
// Response: 202 (Accepted) with the pending certificate row.
// Idempotent: if an issued cert exists with >30d to expiry, return 200 with it.
func (h *sslHandler) enableSSL(c *gin.Context) {
	domainID := c.Param("id")

	// Load and authorize domain
	domain := h.loadDomainOwned(c, domainID)
	if domain == nil {
		return
	}

	ctx := c.Request.Context()

	// Check server_settings.admin_email is configured
	settings, err := h.cfg.ServerSettings.Get(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if settings.AdminEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "missing_admin_email",
			"detail": "server_settings.admin_email must be configured to enable SSL",
		})
		return
	}

	// Check if an issued cert already exists with >30d to expiry
	cert, err := h.cfg.SSLCerts.FindByDomainID(ctx, domainID)
	if err == nil && cert != nil && cert.Status == models.SSLStatusIssued && cert.ExpiresAt != nil {
		daysToExpiry := time.Until(*cert.ExpiresAt).Hours() / 24
		if daysToExpiry > 30 {
			// Idempotent: return 200 with the existing cert
			resp := SSLResponse{
				Status:        cert.Status,
				IssuedAt:      cert.IssuedAt,
				ExpiresAt:     cert.ExpiresAt,
				RenewalCount:  cert.RenewalCount,
				LastRenewedAt: cert.LastRenewedAt,
				LastError:     cert.LastError,
				Staging:       cert.Staging,
				CertPath:      cert.CertPath,
				KeyPath:       cert.KeyPath,
			}
			c.JSON(http.StatusOK, gin.H{"ssl": resp})
			return
		}
	}

	// Set domain.ssl_enabled = true
	domain.SSLEnabled = true
	if err := h.cfg.Domains.Update(ctx, domain); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Create or update certificate row with status=pending
	if cert == nil {
		// Create new certificate row
		cert = &models.SSLCertificate{
			ID:       ids.NewULID(),
			DomainID: domainID,
			Status:   models.SSLStatusPending,
			Staging:  h.cfg.Config.ACME.StagingOnly,
		}
		if err := h.cfg.SSLCerts.Create(ctx, cert); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
	} else {
		// Update existing to pending
		if err := h.cfg.SSLCerts.UpdateStatus(ctx, cert.ID, models.SSLStatusPending, nil); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
	}

	// Schedule reconciliation
	h.cfg.Reconciler.Schedule(domainID)

	resp := SSLResponse{
		Status:        cert.Status,
		IssuedAt:      cert.IssuedAt,
		ExpiresAt:     cert.ExpiresAt,
		RenewalCount:  cert.RenewalCount,
		LastRenewedAt: cert.LastRenewedAt,
		LastError:     cert.LastError,
		Staging:       cert.Staging,
		CertPath:      cert.CertPath,
		KeyPath:       cert.KeyPath,
	}

	c.JSON(http.StatusAccepted, gin.H{"ssl": resp})
}

// disableSSL disables SSL for a domain and revokes the certificate.
// DELETE /api/v1/domains/:id/ssl
// Response: 202 (Accepted).
func (h *sslHandler) disableSSL(c *gin.Context) {
	domainID := c.Param("id")

	// Load and authorize domain
	domain := h.loadDomainOwned(c, domainID)
	if domain == nil {
		return
	}

	ctx := c.Request.Context()

	// Set domain.ssl_enabled = false
	domain.SSLEnabled = false
	if err := h.cfg.Domains.Update(ctx, domain); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Mark certificate row as revoked (if it exists)
	cert, err := h.cfg.SSLCerts.FindByDomainID(ctx, domainID)
	if err == nil && cert != nil {
		if err := h.cfg.SSLCerts.UpdateStatus(ctx, cert.ID, models.SSLStatusRevoked, nil); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
	}

	// Schedule reconciliation
	h.cfg.Reconciler.Schedule(domainID)

	c.Status(http.StatusAccepted)
}

// renewSSL triggers an immediate renewal of an SSL certificate.
// POST /api/v1/domains/:id/ssl/renew
// Admin-only. Response: 202 (Accepted).
func (h *sslHandler) renewSSL(c *gin.Context) {
	domainID := c.Param("id")

	// Load and authorize domain (admin-only)
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	if !claims.IsAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	ctx := c.Request.Context()

	// Load certificate row
	cert, err := h.cfg.SSLCerts.FindByDomainID(ctx, domainID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "no_certificate"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Mark as renewing
	if err := h.cfg.SSLCerts.UpdateStatus(ctx, cert.ID, models.SSLStatusRenewing, nil); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Schedule reconciliation
	h.cfg.Reconciler.Schedule(domainID)

	c.Status(http.StatusAccepted)
}

// listAllSSL lists all SSL certificates across all users (admin-only).
// GET /api/v1/admin/ssl-certificates
func (h *sslHandler) listAllSSL(c *gin.Context) {
	certs, err := h.cfg.SSLCerts.ListAll(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": certs})
}

// listUserSSL lists all SSL certificates for the caller's domains.
// GET /api/v1/ssl-certificates
func (h *sslHandler) listUserSSL(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	certs, err := h.cfg.SSLCerts.ListByUserID(c.Request.Context(), claims.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": certs})
}

// loadDomainOwned fetches the domain by ID and enforces that the
// caller is either the owner or an admin. Returns nil if successful,
// otherwise responds to c and returns nil.
func (h *sslHandler) loadDomainOwned(c *gin.Context, domainID string) *models.Domain {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return nil
	}

	domain, err := h.cfg.Domains.FindByID(c.Request.Context(), domainID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return nil
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return nil
	}

	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return nil
	}

	return domain
}
