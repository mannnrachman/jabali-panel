// M13.1 bandwidth quota event source.
//
// Walks every user with a package whose BandwidthQuotaMB > 0, sums
// their domains' bytes for the current calendar month, and fires
// `bandwidth.quota.warn` when ≥ 80% / `bandwidth.quota.crit` when
// ≥ 100%. Per-user dedupe via shouldFire so a user that's parked
// at 105% doesn't spam notifications hourly.
//
// Cooldowns intentionally match disk_full's 30-min window — the
// notification value is "operator should know", not "operator must
// see this every minute".
package eventsources

import (
	"context"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

const (
	bwQuotaTick    = 30 * time.Minute
	bwQuotaCoolOff = 6 * time.Hour
	bwWarnPercent  = 80.0
	bwCritPercent  = 100.0
)

func runBandwidthQuota(ctx context.Context, d Deps) {
	if d.BWDaily == nil || d.Users == nil || d.Domains == nil || d.Packages == nil {
		return
	}
	d.Log.Info("eventsources: bandwidth_quota started",
		"tick", bwQuotaTick.String(),
		"warn_pct", bwWarnPercent,
		"crit_pct", bwCritPercent)

	// Skip the first tick by 5 min so the bandwidth ticker has had
	// a chance to populate at least one row on a fresh boot.
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Minute):
	}
	bwQuotaPass(ctx, d)

	t := time.NewTicker(bwQuotaTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			bwQuotaPass(ctx, d)
		}
	}
}

func bwQuotaPass(ctx context.Context, d Deps) {
	now := d.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	users, _, err := d.Users.List(ctx, repository.ListOptions{Limit: 1000})
	if err != nil {
		d.Log.Warn("bandwidth_quota: list users failed", "err", err)
		return
	}

	for _, u := range users {
		if u.IsAdmin || u.PackageID == nil || *u.PackageID == "" {
			continue
		}
		pkg, err := d.Packages.FindByID(ctx, *u.PackageID)
		if err != nil || pkg == nil || pkg.BandwidthQuotaMB == 0 {
			continue
		}

		// Sum month-to-date bytes across user's domains.
		bytesByDomain, err := d.BWDaily.SumByDomainForUser(ctx, u.ID, monthStart, now)
		if err != nil {
			d.Log.Warn("bandwidth_quota: sum failed", "user_id", u.ID, "err", err)
			continue
		}

		total, pct, kind, severity := classifyQuota(bytesByDomain, pkg.BandwidthQuotaMB)
		if kind == "" {
			continue
		}
		quotaBytes := uint64(pkg.BandwidthQuotaMB) * 1024 * 1024
		fireBwQuotaEvent(ctx, d, u.ID, total, quotaBytes, pct, kind, severity)
	}
}

// classifyQuota is the pure-logic core of bwQuotaPass: sums per-domain
// bytes, computes the percent-of-quota, and returns the (kind, severity)
// pair that fireBwQuotaEvent should publish — or empty strings when no
// envelope should fire (under the warn threshold OR quota disabled).
//
// Extracted so the calculation can be tested without a live BWDaily
// repo / Queue / History stub.
func classifyQuota(bytesByDomain map[string]uint64, quotaMB uint32) (total uint64, pct float64, kind, severity string) {
	for _, v := range bytesByDomain {
		total += v
	}
	quotaBytes := uint64(quotaMB) * 1024 * 1024
	if quotaBytes == 0 {
		return total, 0, "", ""
	}
	pct = float64(total) / float64(quotaBytes) * 100.0
	switch {
	case pct >= bwCritPercent:
		return total, pct, "bandwidth.quota.crit", "critical"
	case pct >= bwWarnPercent:
		return total, pct, "bandwidth.quota.warn", "warning"
	}
	return total, pct, "", ""
}

func fireBwQuotaEvent(ctx context.Context, d Deps, userID string, used, quota uint64, pct float64, kind, severity string) {
	tag := "user:" + userID
	if !shouldFire(ctx, d, kind, tag, bwQuotaCoolOff) {
		return
	}
	_, err := d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: kind,
		Severity:  severity,
		UserID:    userID,
		Title:     fmt.Sprintf("Bandwidth at %.0f%% of quota", pct),
		Body: fmt.Sprintf(
			"User has used %d MB of %d MB this month (%.1f%%). Domains owned by this user are still serving traffic; the panel does not auto-suspend on quota for v1.",
			used/1024/1024, quota/1024/1024, pct),
		Deeplink: "/jabali-admin/users/" + userID,
	})
	if err != nil {
		d.Log.Warn("eventsources: bandwidth_quota publish failed",
			"user_id", userID, "err", err)
	}
}
