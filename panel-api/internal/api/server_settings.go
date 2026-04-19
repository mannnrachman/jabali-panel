package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

type ServerSettingsHandlerConfig struct {
	Repo  repository.ServerSettingsRepository
	Agent agent.AgentInterface
	Log   *slog.Logger
}

// RegisterServerSettingsRoutes mounts server settings endpoints under the
// given group (typically /api/v1 with auth middleware already applied).
func RegisterServerSettingsRoutes(g *gin.RouterGroup, cfg ServerSettingsHandlerConfig) {
	h := &serverSettingsHandler{cfg: cfg}
	admin := g.Group("/admin/settings")
	admin.Use(middleware.RequireAdmin())
	admin.GET("", h.get)
	admin.PATCH("", h.update)
}

type serverSettingsHandler struct{ cfg ServerSettingsHandlerConfig }

func (h *serverSettingsHandler) get(c *gin.Context) {
	s, err := h.cfg.Repo.Get(c.Request.Context())
	if errors.Is(err, repository.ErrNotFound) {
		// First boot before the seed has run, or a brand-new install
		// where config.toml had no [server] identity to seed from.
		// Return an empty shell instead of 500 so the form loads clean.
		c.JSON(http.StatusOK, &models.ServerSettings{ID: 1})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, s)
}

type updateServerSettingsRequest struct {
	Hostname            *string `json:"hostname,omitempty"`
	PublicIPv4          *string `json:"public_ipv4,omitempty"`
	PublicIPv6          *string `json:"public_ipv6,omitempty"`
	NS1Name             *string `json:"ns1_name,omitempty"`
	NS1IPv4             *string `json:"ns1_ipv4,omitempty"`
	NS2Name             *string `json:"ns2_name,omitempty"`
	NS2IPv4             *string `json:"ns2_ipv4,omitempty"`
	AdminEmail          *string `json:"admin_email,omitempty"`
	Timezone            *string `json:"timezone,omitempty"`
	SSHPort             *uint16 `json:"ssh_port,omitempty"`
	SSHPasswordAuth     *bool   `json:"ssh_password_auth,omitempty"`
	SSHUserPasswordAuth *bool   `json:"ssh_user_password_auth,omitempty"`
}

func (h *serverSettingsHandler) update(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	var req updateServerSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "validation_failed", "detail": err.Error()})
		return
	}

	ctx := c.Request.Context()
	current, err := h.cfg.Repo.Get(ctx)
	if errors.Is(err, repository.ErrNotFound) {
		// No row yet (e.g. seed hadn't run or config was empty at boot).
		// Treat PATCH as initial save so the first admin edit lands cleanly.
		current = &models.ServerSettings{ID: 1}
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	prevHostname := current.Hostname
	prevTimezone := current.Timezone
	prevSSHPort := current.SSHPort
	prevSSHPasswordAuth := current.SSHPasswordAuth
	prevSSHUserPasswordAuth := current.SSHUserPasswordAuth

	if req.Hostname != nil {
		current.Hostname = strings.TrimSpace(*req.Hostname)
	}
	if req.PublicIPv4 != nil {
		current.PublicIPv4 = strings.TrimSpace(*req.PublicIPv4)
	}
	if req.PublicIPv6 != nil {
		current.PublicIPv6 = strings.TrimSpace(*req.PublicIPv6)
	}
	if req.NS1Name != nil {
		current.NS1Name = strings.TrimSpace(*req.NS1Name)
	}
	if req.NS1IPv4 != nil {
		current.NS1IPv4 = strings.TrimSpace(*req.NS1IPv4)
	}
	if req.NS2Name != nil {
		current.NS2Name = strings.TrimSpace(*req.NS2Name)
	}
	if req.NS2IPv4 != nil {
		current.NS2IPv4 = strings.TrimSpace(*req.NS2IPv4)
	}
	if req.AdminEmail != nil {
		current.AdminEmail = strings.TrimSpace(*req.AdminEmail)
	}
	if req.Timezone != nil {
		current.Timezone = strings.TrimSpace(*req.Timezone)
	}
	if req.SSHPort != nil {
		current.SSHPort = *req.SSHPort
	}
	if req.SSHPasswordAuth != nil {
		current.SSHPasswordAuth = *req.SSHPasswordAuth
	}
	if req.SSHUserPasswordAuth != nil {
		current.SSHUserPasswordAuth = *req.SSHUserPasswordAuth
	}

	// Validate — reject obviously bad input so we don't persist garbage.
	if err := validateServerSettings(current); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_settings", "detail": err.Error()})
		return
	}

	if err := h.cfg.Repo.Upsert(ctx, current); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Apply hostname to the OS via agent, best-effort. If the agent isn't
	// reachable we still report success — the DB has the truth and a later
	// reconciliation can sync.
	if current.Hostname != prevHostname && h.cfg.Agent != nil {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := h.cfg.Agent.Call(bgCtx, "system.set_hostname", map[string]any{
				"hostname": current.Hostname,
			}); err != nil {
				h.cfg.Log.Error("agent set_hostname failed", "err", err)
			}
		}()
	}

	// Apply timezone to the OS via agent if changed and not empty.
	if current.Timezone != prevTimezone && current.Timezone != "" && h.cfg.Agent != nil {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := h.cfg.Agent.Call(bgCtx, "system.set_timezone", map[string]any{
				"timezone": current.Timezone,
			}); err != nil {
				h.cfg.Log.Error("agent set_timezone failed", "err", err)
			}
		}()
	}

	// Apply SSH config to the OS via agent if any SSH-affecting field changed.
	// The agent rewrites both /etc/ssh/sshd_config.d/jabali-sshd.conf (global,
	// affects root/admin) and /etc/ssh/sshd_config.d/jabali-sftp.conf (M12
	// Match Group block, affects hosting users), validates with sshd -t,
	// and reloads sshd. See ADR-0028 for the M12 jabali-sftp design.
	if (current.SSHPort != prevSSHPort ||
		current.SSHPasswordAuth != prevSSHPasswordAuth ||
		current.SSHUserPasswordAuth != prevSSHUserPasswordAuth) && h.cfg.Agent != nil {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := h.cfg.Agent.Call(bgCtx, "system.set_ssh_config", map[string]any{
				"port":               current.SSHPort,
				"password_auth":      current.SSHPasswordAuth,
				"user_password_auth": current.SSHUserPasswordAuth,
			}); err != nil {
				h.cfg.Log.Error("agent set_ssh_config failed", "err", err)
			}
		}()
	}

	h.cfg.Log.Info("event=audit kind=server_settings_updated actor_id=" + claims.UserID)
	c.JSON(http.StatusOK, current)
}

// validateServerSettings does lenient input validation matching the installer.
func validateServerSettings(s *models.ServerSettings) error {
	if s.Hostname != "" && !isValidHostname(s.Hostname) {
		return fmt.Errorf("invalid hostname")
	}
	// IPv4
	for label, v := range map[string]string{"public_ipv4": s.PublicIPv4, "ns1_ipv4": s.NS1IPv4, "ns2_ipv4": s.NS2IPv4} {
		if v == "" {
			continue
		}
		if net.ParseIP(v) == nil || net.ParseIP(v).To4() == nil {
			return fmt.Errorf("%s: not a valid IPv4", label)
		}
	}
	// IPv6 (optional)
	if s.PublicIPv6 != "" {
		ip := net.ParseIP(s.PublicIPv6)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("public_ipv6: not a valid IPv6")
		}
	}
	// NS names
	for label, v := range map[string]string{"ns1_name": s.NS1Name, "ns2_name": s.NS2Name} {
		if v == "" {
			continue
		}
		if !isValidHostname(v) {
			return fmt.Errorf("%s: invalid hostname", label)
		}
	}
	// Admin email
	if s.AdminEmail != "" {
		if _, err := mail.ParseAddress(s.AdminEmail); err != nil {
			return fmt.Errorf("admin_email: invalid")
		}
	}
	// Timezone (optional, empty means "use OS default")
	if s.Timezone != "" && !isValidTimezone(s.Timezone) {
		return fmt.Errorf("timezone: invalid format")
	}
	// SSH port
	if s.SSHPort < 1 || s.SSHPort > 65535 {
		return fmt.Errorf("ssh_port: must be between 1 and 65535")
	}
	return nil
}

var (
	hostnameRE  = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)
	timezoneRE  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+)*$`)
)

func isValidHostname(s string) bool {
	if len(s) > 253 {
		return false
	}
	return hostnameRE.MatchString(s)
}

func isValidTimezone(s string) bool {
	if len(s) > 64 {
		return false
	}
	if strings.Contains(s, "..") {
		return false
	}
	if strings.HasPrefix(s, "/") {
		return false
	}
	return timezoneRE.MatchString(s)
}
