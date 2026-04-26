package reconciler

import (
	"context"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// webmail_reconcile.go: per-domain mail.<domain> nginx vhost convergence
// (M6 Step 8). The reconciler walks every domain on each tick; domains
// with email_enabled=1 get their vhost written via the agent, domains
// that are disabled OR missing get their vhost removed. All calls are
// idempotent on the agent side (content-hash gated) so the no-change
// steady state is just one cheap Read per domain.
//
// Why this lives in the reconciler (not the email_enable HTTP handler):
//
//   - The handler flips email_enabled=1 then calls an agent to register
//     DKIM. If the agent-side vhost write fails, we still want the
//     flag flipped — email is on; Bulwark just isn't serving yet.
//     Reconciler catches up on the next tick.
//   - It makes the after-a-restore / after-agent-crash path trivial:
//     the DB is the source of truth, one tick repairs every vhost.
//   - It doesn't couple the HTTP path to nginx reload latency.
//
// SSL note: v1 passes the main domain's cert paths to the agent. That
// cert probably doesn't list mail.<domain> as a SAN — browsers will
// warn. A proper ACME-for-mail.<domain> flow is M6.1 follow-on.

// webmailAgentTimeout bounds each agent call. Matches nginx's own
// reload budget (reloads can take a few seconds on hosts with many
// vhosts because worker shutdown is serial).
const webmailAgentTimeout = 30 * time.Second

// reconcileWebmailVhosts is invoked from ReconcileAll. Errors are
// logged per-domain and don't abort the sweep; the next tick retries.
func (r *Reconciler) reconcileWebmailVhosts(ctx context.Context) {
	if r.sslCerts == nil {
		// Without SSL cert paths we can't render the vhost (ssl_certificate
		// directive is required). In an M5-less install this hook is a
		// no-op — operators running without ACME won't have webmail
		// either.
		return
	}
	if r.domains == nil {
		return
	}

	domains, _, err := r.domains.List(ctx, repository.ListOptions{Limit: 10000})
	if err != nil {
		r.log.Error("webmail reconcile: list domains", "err", err)
		return
	}

	for i := range domains {
		d := &domains[i]
		if d.EmailEnabled {
			r.applyWebmailVhost(ctx, d)
		} else {
			r.removeWebmailVhost(ctx, d.Name)
		}
	}
}

func (r *Reconciler) applyWebmailVhost(ctx context.Context, d *models.Domain) {
	certPath, keyPath, ok := r.webmailSSLPaths(ctx, d.ID)
	if !ok {
		// No live cert on disk yet — the domain's ACME issuance may be
		// in-flight or M5 might not have run. Skip this tick; the next
		// reconcile pass will retry once the cert materialises.
		r.log.Debug("webmail reconcile: skipping vhost — no usable SSL cert on file",
			"domain_id", d.ID, "domain", d.Name)
		return
	}

	callCtx, cancel := context.WithTimeout(ctx, webmailAgentTimeout)
	defer cancel()
	params := map[string]any{
		"domain_name":   d.Name,
		"ssl_cert_path": certPath,
		"ssl_key_path":  keyPath,
		// doc_root is the ACME HTTP-01 webroot for renewals targeting
		// mail.<domain>. Same path ssl.issue uses (-w domain.DocRoot)
		// so renewal challenge files land where nginx will serve them.
		"doc_root": d.DocRoot,
	}
	// listen_ipv4 / listen_ipv6 — same resolution as the apex vhost
	// (M24). When the apex vhost binds a specific IP, the mail vhost
	// MUST also bind that IP or it falls into nginx's wildcard pool
	// (which gets ignored on IPs with at least one specific listener)
	// and SNI for mail.<domain> lands on the wrong tenant's cert.
	if r.managedIPs != nil {
		if v4 := r.resolveListenIPAddress(ctx, d.ListenIPv4ID, "ipv4"); v4 != "" {
			params["listen_ipv4"] = v4
		}
		if v6 := r.resolveListenIPAddress(ctx, d.ListenIPv6ID, "ipv6"); v6 != "" {
			params["listen_ipv6"] = v6
		}
	}
	if _, err := r.agent.Call(callCtx, "webmail.vhost_apply", params); err != nil {
		r.log.Error("webmail reconcile: vhost_apply failed",
			"domain_id", d.ID, "domain", d.Name, "err", err)
	}
}

func (r *Reconciler) removeWebmailVhost(ctx context.Context, domainName string) {
	if domainName == "" {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, webmailAgentTimeout)
	defer cancel()
	if _, err := r.agent.Call(callCtx, "webmail.vhost_remove", map[string]any{
		"domain_name": domainName,
	}); err != nil {
		// The remove path is idempotent; a failure here usually means
		// nginx itself is down. Log and move on — next tick retries.
		r.log.Error("webmail reconcile: vhost_remove failed",
			"domain", domainName, "err", err)
	}
}

// webmailSSLPaths returns the cert + key paths for a domain if a usable
// certificate is on file. Mirrors the allow-list used by ReconcileOne
// when rendering the main vhost: accept anything not REVOKED with both
// paths populated.
func (r *Reconciler) webmailSSLPaths(ctx context.Context, domainID string) (string, string, bool) {
	sslCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cert, err := r.sslCerts.FindByDomainID(sslCtx, domainID)
	if err != nil || cert == nil {
		return "", "", false
	}
	if cert.Status == models.SSLStatusRevoked {
		return "", "", false
	}
	if cert.CertPath == nil || cert.KeyPath == nil {
		return "", "", false
	}
	return *cert.CertPath, *cert.KeyPath, true
}
