package eventsources

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

const (
	diskQuotaTick    = 30 * time.Minute
	diskQuotaCoolOff = 6 * time.Hour
	// Fire when used / hard ≥ 90%. Hard cap is the "you can't write
	// any more" boundary; warning before that gives the user time to
	// clean up before write failures hit.
	diskQuotaPercent = 90.0
)

// runDiskQuota iterates every hosting user (those with a non-empty
// username, i.e. excluded admins) every 30 minutes, asks the agent
// for their current disk-quota report, and fires a per-user
// envelope when used / hard ≥ 90%. 6-hour cooldown per user so a
// chronically-near-full account doesn't spam.
//
// No-op when Users repo, Agent, or QuotaMount aren't wired (CI/dev
// boxes without /home as a separate fs).
func runDiskQuota(ctx context.Context, d Deps) {
	if d.Users == nil || d.Agent == nil || d.QuotaMount == "" {
		d.Log.Debug("eventsources: disk_quota disabled — missing Users/Agent/QuotaMount")
		return
	}
	// One-shot at boot.
	diskQuotaPass(ctx, d)
	tick := time.NewTicker(diskQuotaTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		diskQuotaPass(ctx, d)
	}
}

type quotaReportShape struct {
	Disk *struct {
		UsedKB  uint64 `json:"used_kb"`
		LimitKB uint64 `json:"limit_kb"`
	} `json:"disk,omitempty"`
}

func diskQuotaPass(ctx context.Context, d Deps) {
	users, _, err := d.Users.List(ctx, repository.ListOptions{Limit: 5000})
	if err != nil {
		d.Log.Warn("eventsources: disk_quota list users failed", "err", err)
		return
	}
	for _, u := range users {
		if u.Username == nil || *u.Username == "" {
			continue
		}
		// Per-user agent call. 5s timeout — agent quota read is
		// microseconds, the timeout is for the unix socket
		// round-trip under load.
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		raw, err := d.Agent.Call(callCtx, "user.limits.report", map[string]any{
			"username":    *u.Username,
			"quota_mount": d.QuotaMount,
		})
		cancel()
		if err != nil {
			// Common case: user not yet provisioned on disk —
			// quota tools return non-zero. Quiet skip.
			continue
		}
		var rep quotaReportShape
		if err := json.Unmarshal(raw, &rep); err != nil {
			continue
		}
		if rep.Disk == nil || rep.Disk.LimitKB == 0 {
			continue // unlimited or unconfigured — nothing to alert on
		}
		pct := float64(rep.Disk.UsedKB) / float64(rep.Disk.LimitKB) * 100.0
		if pct < diskQuotaPercent {
			continue
		}
		tag := "user:" + *u.Username
		if !shouldFire(ctx, d, "disk.quota.warn", tag, diskQuotaCoolOff) {
			continue
		}
		_, err = d.Queue.Publish(ctx, notifications.Envelope{
			EventKind: "disk.quota.warn",
			Severity:  models.NotificationSeverityWarning,
			Title:     fmt.Sprintf("%s at %.0f%% of disk quota", *u.Username, pct),
			Body: fmt.Sprintf(
				"User %s used %d KB of %d KB hard limit (%.1f%%). (%s)",
				*u.Username, rep.Disk.UsedKB, rep.Disk.LimitKB, pct, tag,
			),
			Deeplink: "/jabali-admin/users",
			UserID:   u.ID,
		})
		if err != nil {
			d.Log.Warn("eventsources: publish disk.quota.warn failed", "user", *u.Username, "err", err)
		}
	}
}
