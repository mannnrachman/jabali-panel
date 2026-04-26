package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/services"
)

// AdminPanelCertificateHandlerConfig wires the dependencies for the
// M32 panel-cert REST surface. PanelCerts is the singleton repo;
// ServerSettings provides hostname + admin_email + public_ipv4 (the
// inputs to both the routability gate and the certbot invocation);
// Agent dispatches ssl.panel.issue; Routability is the gate
// implementation (separate so tests can stub it).
type AdminPanelCertificateHandlerConfig struct {
	PanelCerts     repository.PanelCertificateRepository
	ServerSettings repository.ServerSettingsRepository
	Agent          agent.AgentInterface
	Routability    *services.PanelCertRoutability
}

// RegisterAdminPanelCertificateRoutes mounts:
//
//	GET  /admin/panel-certificate          — current state + routability
//	POST /admin/panel-certificate/toggle   — flip use_le / staging flags
//	POST /admin/panel-certificate/issue    — force an immediate attempt
//
// All gated by RequireAdmin. The reconciler picks up state changes
// on its own tick; toggle/issue endpoints can also synchronously
// fan out to the agent so the admin sees an immediate response.
func RegisterAdminPanelCertificateRoutes(g *gin.RouterGroup, cfg AdminPanelCertificateHandlerConfig) {
	if cfg.PanelCerts == nil || cfg.ServerSettings == nil {
		return
	}
	if cfg.Routability == nil {
		cfg.Routability = services.NewPanelCertRoutability()
	}
	h := &adminPanelCertHandler{cfg: cfg}
	grp := g.Group("/admin/panel-certificate")
	grp.Use(middleware.RequireAdmin())
	grp.GET("", h.get)
	grp.POST("/toggle", h.toggle)
	grp.POST("/issue", h.issue)
}

type adminPanelCertHandler struct{ cfg AdminPanelCertificateHandlerConfig }

// panelCertGetResponse layers the live routability check on top of the
// stored row so the UI never needs a separate request to render the
// "Use Let's Encrypt" toggle's enabled/disabled state.
type panelCertGetResponse struct {
	*models.PanelCertificate
	Routable        bool   `json:"routable"`
	RoutableReason  string `json:"routable_reason,omitempty"`
}

func (h *adminPanelCertHandler) get(c *gin.Context) {
	ctx := c.Request.Context()
	settings, err := h.cfg.ServerSettings.Get(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "settings_load_failed", "details": err.Error()})
		return
	}
	row, err := h.cfg.PanelCerts.EnsureDefault(ctx, settings.Hostname)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "panel_cert_load_failed", "details": err.Error()})
		return
	}
	res := panelCertGetResponse{PanelCertificate: row}
	rr, _ := h.cfg.Routability.Check(ctx, settings.Hostname, settings.PublicIPv4)
	res.Routable = rr.Routable
	res.RoutableReason = rr.Reason
	c.JSON(http.StatusOK, res)
}

// panelCertToggleRequest is intentionally pointer-typed so callers
// can flip one flag without specifying the other. PATCH-style
// semantics on a singleton row.
type panelCertToggleRequest struct {
	UseLE   *bool `json:"use_le,omitempty"`
	Staging *bool `json:"staging,omitempty"`
}

func (h *adminPanelCertHandler) toggle(c *gin.Context) {
	var req panelCertToggleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_json", "details": err.Error()})
		return
	}
	if req.UseLE == nil && req.Staging == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no_fields_to_update"})
		return
	}
	ctx := c.Request.Context()
	settings, err := h.cfg.ServerSettings.Get(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "settings_load_failed"})
		return
	}
	row, err := h.cfg.PanelCerts.EnsureDefault(ctx, settings.Hostname)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "panel_cert_load_failed"})
		return
	}
	if req.UseLE != nil {
		row.UseLE = *req.UseLE
		// Turning off use_le doesn't tear down the existing LE cert
		// — we leave it in place until expiry, then provision_tls_cert
		// regenerates self-signed at next install.sh run. Avoids cert
		// churn for an admin who toggles by accident.
		if !*req.UseLE && row.Status == models.PanelCertStatusPendingACMERetry {
			// Stop the retry loop when admin gives up. Status flips
			// to self_signed so the reconciler skips it; cert files
			// on disk are untouched.
			row.Status = models.PanelCertStatusSelfSigned
			row.NextRetryAt = nil
		}
	}
	if req.Staging != nil {
		row.Staging = *req.Staging
	}
	if err := h.cfg.PanelCerts.Upsert(ctx, row); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "panel_cert_save_failed", "details": err.Error()})
		return
	}
	c.JSON(http.StatusOK, row)
}

// issue forces an immediate ssl.panel.issue dispatch, bypassing the
// reconciler's next_retry_at backoff. The status row is updated based
// on the agent response so the UI's TanStack Query refetch picks up
// the result without waiting on the reconciler tick.
func (h *adminPanelCertHandler) issue(c *gin.Context) {
	ctx := c.Request.Context()
	settings, err := h.cfg.ServerSettings.Get(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "settings_load_failed"})
		return
	}
	if settings.Hostname == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_hostname", "details": "set Server Settings → General → Hostname first"})
		return
	}
	if settings.AdminEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_admin_email", "details": "set Server Settings → General → Admin email first (required for Let's Encrypt registration)"})
		return
	}
	rr, _ := h.cfg.Routability.Check(ctx, settings.Hostname, settings.PublicIPv4)
	if !rr.Routable {
		c.JSON(http.StatusFailedDependency, gin.H{"error": "not_routable", "details": rr.Reason})
		return
	}
	row, err := h.cfg.PanelCerts.EnsureDefault(ctx, settings.Hostname)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "panel_cert_load_failed"})
		return
	}

	// Move the row into pending_acme so a concurrent reconciler tick
	// doesn't double-fire the agent call.
	row.Status = models.PanelCertStatusPendingACME
	if err := h.cfg.PanelCerts.Upsert(ctx, row); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "panel_cert_save_failed"})
		return
	}

	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_unavailable"})
		return
	}
	dispatchCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	// See panel_certificate_reconciler.go for why extra_hostnames is
	// empty — mail.<panel-hostname> DNS isn't auto-provisioned, so
	// including it as a SAN unconditionally fails every fresh install.
	raw, agentErr := h.cfg.Agent.Call(dispatchCtx, "ssl.panel.issue", map[string]any{
		"hostname":        settings.Hostname,
		"extra_hostnames": []string{},
		"email":           settings.AdminEmail,
		"staging":         row.Staging,
	})
	if agentErr != nil {
		_ = h.cfg.PanelCerts.MarkPendingRetry(ctx, agentErr.Error(), 3*time.Hour)
		c.JSON(http.StatusBadGateway, gin.H{"error": "issue_failed", "details": agentErr.Error()})
		return
	}

	// Parse the agent envelope back into the shape we know — the
	// agent returns issued_at + expires_at as ISO8601Z strings.
	var resp struct {
		IssuedAt  string `json:"issued_at"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		_ = h.cfg.PanelCerts.MarkPendingRetry(ctx, "failed to parse agent response: "+err.Error(), 3*time.Hour)
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_parse_failed"})
		return
	}
	issuedAt, err1 := time.Parse(time.RFC3339, resp.IssuedAt)
	expiresAt, err2 := time.Parse(time.RFC3339, resp.ExpiresAt)
	if err1 != nil || err2 != nil {
		_ = h.cfg.PanelCerts.MarkPendingRetry(ctx, "failed to parse agent timestamps", 3*time.Hour)
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_timestamp_parse_failed"})
		return
	}
	if err := h.cfg.PanelCerts.MarkIssued(ctx, issuedAt, expiresAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "mark_issued_failed", "details": err.Error()})
		return
	}
	updated, _ := h.cfg.PanelCerts.Get(ctx)
	if updated == nil {
		updated = row
	}
	c.JSON(http.StatusOK, updated)
}

// errPanelCertNoSettings is exported for the reconciler's logging path
// so it can distinguish a routability skip from a real failure.
var errPanelCertNoSettings = errors.New("server settings not initialised")
