package eventsources

import (
	"context"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// Cadence + thresholds for the cert-renew watcher.
const (
	certRenewTick    = time.Hour
	certExpiry7d     = 7 * 24 * time.Hour
	certExpiry1d     = 24 * time.Hour
	certRenewCoolOff = 24 * time.Hour
)

// runCertRenew fires three event families:
//
//   - domain.expiry.7d / domain.expiry.1d — cert crosses into the
//     pre-expiry window without a successful renewal bumping ExpiresAt
//     outward.
//   - cert.renew.fail — cert's Status is "failed" with a LastError.
//   - cert.renew.ok — cert's LastRenewedAt landed within the last
//     tick window and Status flipped to "active". Informational
//     confirmation; off by default.
//
// Doesn't drive renewals itself — the SSL reconciler owns that. This
// source only watches and reports.
func runCertRenew(ctx context.Context, d Deps) {
	if d.SSLCerts == nil {
		d.Log.Debug("eventsources: cert_renew disabled (no SSLCerts repo)")
		return
	}
	// Fire a pass immediately so a freshly-restarted panel-api doesn't
	// stay blind for an hour after a renewal that completed during the
	// downtime.
	certRenewPass(ctx, d)
	tick := time.NewTicker(certRenewTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		certRenewPass(ctx, d)
	}
}

func certRenewPass(ctx context.Context, d Deps) {
	rows, err := d.SSLCerts.ListAll(ctx)
	if err != nil {
		d.Log.Warn("eventsources: cert_renew list failed", "err", err)
		return
	}
	now := d.Now()
	for _, r := range rows {
		if r.ExpiresAt != nil {
			remaining := r.ExpiresAt.Sub(now)
			switch {
			case remaining <= certExpiry1d && remaining > 0:
				firePreExpiry(ctx, d, r, "domain.expiry.1d", models.NotificationSeverityError)
			case remaining <= certExpiry7d && remaining > certExpiry1d:
				firePreExpiry(ctx, d, r, "domain.expiry.7d", models.NotificationSeverityWarning)
			}
		}
		if r.Status == "failed" && r.LastError != nil && *r.LastError != "" {
			fireRenewFail(ctx, d, r)
		}
		if r.Status == "active" && r.LastRenewedAt != nil {
			// Window of one tick + a touch of slack covers the case where
			// the renewer finished just before this pass. The dedupe tag
			// embeds the timestamp so consecutive passes don't republish.
			age := now.Sub(*r.LastRenewedAt)
			if age >= 0 && age <= certRenewTick+5*time.Minute {
				fireRenewOK(ctx, d, r)
			}
		}
	}
}

func firePreExpiry(ctx context.Context, d Deps, cert repository.SSLCertificateWithDomain, kind, severity string) {
	tag := "cert:" + cert.ID
	if !shouldFire(ctx, d, kind, tag, certRenewCoolOff) {
		return
	}
	remaining := cert.ExpiresAt.Sub(d.Now())
	_, err := d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: kind,
		Severity:  severity,
		Title:     fmt.Sprintf("SSL cert for %s expires in %s", cert.DomainName, humanizeDuration(remaining)),
		Body:      fmt.Sprintf("Certificate %s for %s expires %s. (%s)", cert.ID, cert.DomainName, cert.ExpiresAt.UTC().Format(time.RFC3339), tag),
		Deeplink:  "/admin/ssl",
	})
	if err != nil {
		d.Log.Warn("eventsources: publish pre-expiry failed", "event_kind", kind, "err", err)
	}
}

func fireRenewFail(ctx context.Context, d Deps, cert repository.SSLCertificateWithDomain) {
	tag := "cert:" + cert.ID
	if !shouldFire(ctx, d, "cert.renew.fail", tag, certRenewCoolOff) {
		return
	}
	errMsg := ""
	if cert.LastError != nil {
		errMsg = *cert.LastError
	}
	_, err := d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: "cert.renew.fail",
		Severity:  models.NotificationSeverityError,
		Title:     fmt.Sprintf("SSL cert renewal failed for %s", cert.DomainName),
		Body:      fmt.Sprintf("Renewal error: %s. (%s)", errMsg, tag),
		Deeplink:  "/admin/ssl",
	})
	if err != nil {
		d.Log.Warn("eventsources: publish renew-fail failed", "err", err)
	}
}

func fireRenewOK(ctx context.Context, d Deps, cert repository.SSLCertificateWithDomain) {
	// Tag includes the renewal timestamp so each unique renewal fires
	// at most once across passes — replays of an "active" row with the
	// same LastRenewedAt are deduped.
	tag := fmt.Sprintf("cert:%s:renewed_at:%s", cert.ID, cert.LastRenewedAt.UTC().Format(time.RFC3339))
	if !shouldFire(ctx, d, "cert.renew.ok", tag, certRenewCoolOff) {
		return
	}
	expiresStr := ""
	if cert.ExpiresAt != nil {
		expiresStr = cert.ExpiresAt.UTC().Format(time.RFC3339)
	}
	_, err := d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: "cert.renew.ok",
		Severity:  models.NotificationSeverityInfo,
		Title:     fmt.Sprintf("SSL cert renewed for %s", cert.DomainName),
		Body:      fmt.Sprintf("Renewed at %s. New expiry %s. (%s)", cert.LastRenewedAt.UTC().Format(time.RFC3339), expiresStr, tag),
		Deeplink:  "/admin/ssl",
	})
	if err != nil {
		d.Log.Warn("eventsources: publish renew-ok failed", "err", err)
	}
}

func humanizeDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
