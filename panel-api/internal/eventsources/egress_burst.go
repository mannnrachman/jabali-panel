package eventsources

import (
	"context"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

// egressBurstTick is how often the source polls per-user drop counts.
// Reconciler updates drop_count_24h every ~60s, so anything finer wastes
// cycles; matching the reconciler cadence is the natural choice.
const egressBurstTick = 60 * time.Second

// egressBurstCoolOff debounces per-user fires so a sustained webshell
// doesn't drown the operator's M14 channel. Same window as crowdsec_spike
// — long enough that a real attack still produces follow-up alerts every
// 15 min, short enough that a fresh burst after dawn registers as a new
// event, not a continuation.
const egressBurstCoolOff = 15 * time.Minute

// runEgressBurst polls user_egress_policies every minute and fires
// egress.drop.burst whenever a user's drop_count_24h (which the
// reconciler currently fills with per-tick deltas) crosses the
// operator-tunable threshold (server_settings.egress_burst_threshold,
// default 50). Disabled when either repo is unwired.
func runEgressBurst(ctx context.Context, d Deps) {
	if d.UserEgressPolicies == nil || d.ServerSettings == nil {
		d.Log.Info("eventsources: egress.drop.burst disabled (missing repos)")
		return
	}
	d.Log.Info("eventsources: egress.drop.burst started", "tick", egressBurstTick.String())
	egressBurstPass(ctx, d)
	tick := time.NewTicker(egressBurstTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		egressBurstPass(ctx, d)
	}
}

func egressBurstPass(ctx context.Context, d Deps) {
	settings, err := d.ServerSettings.Get(ctx)
	if err != nil || settings == nil {
		d.Log.Debug("egress.drop.burst: server_settings unavailable", "err", err)
		return
	}
	threshold := uint64(settings.EgressBurstThreshold)
	if threshold == 0 {
		threshold = 50
	}

	policies, err := d.UserEgressPolicies.List(ctx)
	if err != nil {
		d.Log.Debug("egress.drop.burst: list policies failed", "err", err)
		return
	}

	for _, p := range policies {
		if p.DropCount24h < threshold {
			continue
		}
		dedupe := fmt.Sprintf("user=%s drops>=%d", p.UserID, threshold)
		if !shouldFire(ctx, d, "egress.drop.burst", dedupe, egressBurstCoolOff) {
			continue
		}
		_, err := d.Queue.Publish(ctx, notifications.Envelope{
			EventKind: "egress.drop.burst",
			Severity:  models.NotificationSeverityWarning,
			Title:     fmt.Sprintf("Egress burst: %d drops in last tick", p.DropCount24h),
			Body: fmt.Sprintf(
				"User %s exceeded egress drop threshold (%d ≥ %d). Likely indicates a webshell or compromised script trying to phone home.",
				p.UserID, p.DropCount24h, threshold,
			),
			Deeplink: "/admin/security?tab=egress",
		})
		if err != nil {
			d.Log.Warn("egress.drop.burst: publish failed", "err", err, "user_id", p.UserID)
		}
	}
}
