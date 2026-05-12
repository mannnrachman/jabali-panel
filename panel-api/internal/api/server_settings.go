package api

import (
	"context"
	"encoding/json"
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
	ctx := c.Request.Context()
	s, err := h.cfg.Repo.Get(ctx)
	if errors.Is(err, repository.ErrNotFound) {
		// First boot before the seed has run, or a brand-new install
		// where config.toml had no [server] identity to seed from.
		// Return an empty shell instead of 500 so the form loads clean.
		s = &models.ServerSettings{ID: 1}
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Timezone fallback: when the DB column is empty (admin never picked
	// one yet), surface the OS-configured zone so the UI dropdown shows
	// the actual current value instead of blank. Doesn't write back —
	// the row stays empty until the admin saves explicitly.
	if s.Timezone == "" && h.cfg.Agent != nil {
		infoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if raw, err := h.cfg.Agent.Call(infoCtx, "system.info", nil); err == nil {
			var info struct {
				Timezone string `json:"timezone"`
			}
			if jsonErr := json.Unmarshal(raw, &info); jsonErr == nil && info.Timezone != "" {
				s.Timezone = info.Timezone
			}
		} else {
			h.cfg.Log.Debug("agent system.info failed during timezone fallback", "err", err)
		}
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
	PanelBrandText      *string `json:"panel_brand_text,omitempty"`
	DiskQuotaEnabled    *bool   `json:"disk_quota_enabled,omitempty"`
	BandwidthQuotaEnforceEnabled *bool `json:"bandwidth_quota_enforce_enabled,omitempty"`
	UploadMaxSizeMB     *uint32 `json:"upload_max_size_mb,omitempty"`

	// M13 SSH shell sandbox.
	SSHSandboxMode            *string `json:"ssh_sandbox_mode,omitempty"`
	DefaultNspawnImageVersion *string `json:"default_nspawn_image_version,omitempty"`

	// M30.1 backup concurrency. Per-schedule retention is the source
	// of truth — server-wide keep_* fields exist on the row but are
	// no longer writeable from the API.
	BackupMaxConcurrentJobs *uint32 `json:"backup_max_concurrent_jobs,omitempty"`

	// M37 PostgreSQL parity (ADR-0091). PostgresEnabled gates the
	// /databases POST handler from accepting engine="postgres" + the
	// reconciler from starting the postgresql service.
	PostgresEnabled               *bool   `json:"postgres_enabled,omitempty"`
	PostgresMaxConnectionsPerUser *uint16 `json:"postgres_max_connections_per_user,omitempty"`

	// M35 SSRF override. When true, migrate.ValidateHost accepts
	// RFC1918 / loopback / link-local targets. Operator opts in for
	// local-network test migrations; default-deny remains the safe
	// production stance. ADR-0095 decision 8.
	MigrationAllowPrivateHosts *bool `json:"migration_allow_private_hosts,omitempty"`
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
	prevPostgresEnabled := current.PostgresEnabled
	prevTimezone := current.Timezone
	prevSSHPort := current.SSHPort
	prevSSHPasswordAuth := current.SSHPasswordAuth
	prevSSHUserPasswordAuth := current.SSHUserPasswordAuth
	prevSSHSandboxMode := current.SSHSandboxMode
	prevDefaultNspawnImageVersion := current.DefaultNspawnImageVersion

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
	if req.PanelBrandText != nil {
		current.PanelBrandText = strings.TrimSpace(*req.PanelBrandText)
	}
	if req.DiskQuotaEnabled != nil {
		current.DiskQuotaEnabled = *req.DiskQuotaEnabled
	}
	if req.BandwidthQuotaEnforceEnabled != nil {
		current.BandwidthQuotaEnforceEnabled = *req.BandwidthQuotaEnforceEnabled
	}
	if req.UploadMaxSizeMB != nil {
		current.UploadMaxSizeMB = *req.UploadMaxSizeMB
	}
	if req.SSHSandboxMode != nil {
		current.SSHSandboxMode = strings.TrimSpace(*req.SSHSandboxMode)
	}
	if req.DefaultNspawnImageVersion != nil {
		// Reject empty string — keeps UIs from clobbering a valid pin
		// when the field is hidden / not yet loaded on submit. To clear,
		// the operator must change the column DEFAULT in a migration.
		v := strings.TrimSpace(*req.DefaultNspawnImageVersion)
		if v != "" {
			current.DefaultNspawnImageVersion = v
		}
	}
	if req.BackupMaxConcurrentJobs != nil {
		// 0 acts as "use default" in the dispatcher. Cap at 64 — even a
		// single restic chain at 8 cores tops out the IO ceiling well
		// before then.
		v := *req.BackupMaxConcurrentJobs
		if v > 64 {
			v = 64
		}
		current.BackupMaxConcurrentJobs = v
	}
	if req.PostgresEnabled != nil {
		current.PostgresEnabled = *req.PostgresEnabled
	}
	if req.MigrationAllowPrivateHosts != nil {
		current.MigrationAllowPrivateHosts = *req.MigrationAllowPrivateHosts
	}
	if req.PostgresMaxConnectionsPerUser != nil {
		v := *req.PostgresMaxConnectionsPerUser
		if v == 0 {
			v = 25
		}
		if v > 1000 {
			v = 1000
		}
		current.PostgresMaxConnectionsPerUser = v
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

	// M37 Phase 4: Postgres opt-in. flip true → install + start;
	// flip false → stop + disable (data preserved). Dispatch in
	// the background — install can take up to ~60s on apt-get and
	// the PATCH should not block the UI for that long. The DB row
	// already reflects the operator intent; reconcile will eventually
	// catch up on retry.
	if current.PostgresEnabled != prevPostgresEnabled && h.cfg.Agent != nil {
		go func(target bool) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			method := "db.postgres.disable"
			if target {
				method = "db.postgres.install"
			}
			if _, err := h.cfg.Agent.Call(bgCtx, method, map[string]any{}); err != nil {
				h.cfg.Log.Error("agent postgres lifecycle failed",
					"method", method, "err", err)
			}
		}(current.PostgresEnabled)
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

	// Sandbox mode + default-image flips: write the matching files on
	// disk so the wrapper picks them up on the next connect. No sshd
	// reload needed (wrapper reads on every exec).
	if (current.SSHSandboxMode != prevSSHSandboxMode ||
		current.DefaultNspawnImageVersion != prevDefaultNspawnImageVersion) && h.cfg.Agent != nil {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := h.cfg.Agent.Call(bgCtx, "system.set_ssh_sandbox_mode", map[string]any{
				"mode":          current.SSHSandboxMode,
				"default_image": current.DefaultNspawnImageVersion,
			}); err != nil {
				h.cfg.Log.Error("agent set_ssh_sandbox_mode failed", "err", err)
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
	// Panel brand text: free-form but capped at 60 chars.
	if len(s.PanelBrandText) > 60 {
		return fmt.Errorf("panel_brand_text: must be <= 60 chars")
	}
	// Upload cap. 0 == "use compile-time default (1 GB)"; otherwise
	// 1 MB minimum and 10 GB ceiling (matches the practical browser-
	// upload limit and the nginx vhost client_max_body_size; admins
	// wanting bigger should use SFTP/SCP).
	if s.UploadMaxSizeMB != 0 && (s.UploadMaxSizeMB < 1 || s.UploadMaxSizeMB > 10240) {
		return fmt.Errorf("upload_max_size_mb: must be 0 or between 1 and 10240")
	}
	// M13 SSH sandbox.
	if s.SSHSandboxMode != "" && s.SSHSandboxMode != "bubblewrap" && s.SSHSandboxMode != "nspawn" {
		return fmt.Errorf("ssh_sandbox_mode: must be 'bubblewrap' or 'nspawn'")
	}
	if s.DefaultNspawnImageVersion != "" && !isImageNamePattern(s.DefaultNspawnImageVersion) {
		return fmt.Errorf("default_nspawn_image_version: must match [a-z0-9-]+")
	}
	return nil
}

// isImageNamePattern matches the [a-z0-9-]+ shape used everywhere
// from agent helpers to wrapper scripts. Kept inline here so the
// file doesn't depend on regexp at build time for one validation.
func isImageNamePattern(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	return true
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
