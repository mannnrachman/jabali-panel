package reconciler

import (
	"context"
	"encoding/json"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// reconcilePanelCertificate is M32's reconciler hook. Runs once per
// ReconcileAll tick. Owns the state machine for the singleton
// panel_certificate row and dispatches ssl.panel.issue when LE is
// enabled and the routability gate clears.
//
// Decision table (per tick):
//
//	use_le=0                                                    → noop
//	hostname empty / admin_email empty                          → noop (admin must finish General settings first)
//	routability gate fails                                       → noop, log reason
//	status=self_signed       → set pending_acme + dispatch
//	status=pending_acme_retry + next_retry_at <= now → dispatch
//	status=issued + expires_at within 30d                       → noop (certbot's own timer renews; deploy-hook updates row)
//	all other states                                            → noop
//
// On agent success: MarkIssued (status=issued, attempt_count=0).
// On agent failure: MarkPendingRetry (status=pending_acme_retry,
// attempt_count++, next_retry_at=now+3h).
func (r *Reconciler) reconcilePanelCertificate(ctx context.Context) {
	if r.panelCerts == nil || r.panelCertRoutability == nil || r.serverSettings == nil {
		return
	}

	settings, err := r.serverSettings.Get(ctx)
	if err != nil {
		r.log.Debug("panel-cert reconcile skipped: server_settings unavailable", "error", err)
		return
	}

	row, err := r.panelCerts.EnsureDefault(ctx, settings.Hostname)
	if err != nil {
		r.log.Warn("panel-cert reconcile failed to load row", "error", err)
		return
	}
	if !row.UseLE {
		return
	}

	switch row.Status {
	case models.PanelCertStatusSelfSigned:
		// First time the admin enabled use_le on a routable hostname.
		// Fall through to dispatch.
	case models.PanelCertStatusPendingACMERetry:
		if row.NextRetryAt == nil || row.NextRetryAt.After(time.Now()) {
			return
		}
	case models.PanelCertStatusPendingACME:
		// Another reconciler tick (or the admin's force-issue REST
		// path) already kicked the agent. Skip — we'll see the
		// terminal state on the next tick.
		return
	case models.PanelCertStatusIssued:
		// certbot's daily timer handles renewal; deploy-hook updates
		// the row out-of-band. The reconciler is only the "first
		// issue" + "retry on failure" driver.
		return
	case models.PanelCertStatusFailed:
		// Terminal until admin clears it via the toggle UI. M32.1
		// surfaces a Reset button.
		return
	}

	if settings.Hostname == "" || settings.AdminEmail == "" {
		r.log.Debug("panel-cert reconcile skipped: hostname or admin_email empty")
		return
	}
	gate, err := r.panelCertRoutability.Check(ctx, settings.Hostname, settings.PublicIPv4)
	if err != nil {
		r.log.Warn("panel-cert routability check errored", "error", err)
		return
	}
	if !gate.Routable {
		r.log.Debug("panel-cert reconcile skipped: not routable", "reason", gate.Reason)
		return
	}

	// Mark in-flight before the agent call so a concurrent reconciler
	// tick (or the REST issue endpoint) doesn't double-dispatch.
	row.Status = models.PanelCertStatusPendingACME
	if err := r.panelCerts.Upsert(ctx, row); err != nil {
		r.log.Warn("panel-cert reconcile pre-dispatch upsert failed", "error", err)
		return
	}

	dispatchCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	// extra_hostnames left empty: the mail.<panel-hostname> SAN was in
	// the original M32 plan but every fresh install fails because the
	// DNS record for mail.<panel-hostname> doesn't exist by default —
	// the operator's DNS provider hosts the panel hostname zone, not
	// pdns. Bulwark on the panel hostname is reachable through the
	// /webmail redirect (ADR-0048) and through per-domain
	// mail.<user-domain> certs. Re-introduce the SAN as opt-in once
	// the DNS provisioning side lands.
	raw, agentErr := r.agent.Call(dispatchCtx, "ssl.panel.issue", map[string]any{
		"hostname":        settings.Hostname,
		"extra_hostnames": []string{},
		"email":           settings.AdminEmail,
		"staging":         row.Staging,
	})
	if agentErr != nil {
		r.log.Warn("panel-cert ssl.panel.issue failed", "error", agentErr, "hostname", settings.Hostname)
		_ = r.panelCerts.MarkPendingRetry(ctx, agentErr.Error(), 3*time.Hour)
		return
	}

	var resp struct {
		IssuedAt  string `json:"issued_at"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		r.log.Warn("panel-cert agent response unmarshal failed", "error", err)
		_ = r.panelCerts.MarkPendingRetry(ctx, "agent response unmarshal: "+err.Error(), 3*time.Hour)
		return
	}
	issuedAt, err1 := time.Parse(time.RFC3339, resp.IssuedAt)
	expiresAt, err2 := time.Parse(time.RFC3339, resp.ExpiresAt)
	if err1 != nil || err2 != nil {
		_ = r.panelCerts.MarkPendingRetry(ctx, "agent response timestamp parse failed", 3*time.Hour)
		return
	}
	if err := r.panelCerts.MarkIssued(ctx, issuedAt, expiresAt); err != nil {
		r.log.Warn("panel-cert MarkIssued failed", "error", err)
		return
	}
	r.log.Info("panel-cert issued", "hostname", settings.Hostname, "expires_at", expiresAt)
}
