package eventsources

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

// aideTamperTick polls the agent's AIDE status periodically. AIDE
// itself only checks daily (jabali-aide-check.timer), so polling more
// often than every 5 minutes wastes cycles. We pick 5 minutes because
// it's the shortest cadence at which a 24h cooldown produces predictable
// behaviour (one alert per check run, never bursting).
const aideTamperTick = 5 * time.Minute

// aideTamperCooldown debounces per-host fires. AIDE's daily check
// produces one report per day; cooldown longer than the run gap is
// what we want.
const aideTamperCooldown = 24 * time.Hour

// criticalPaths short-circuit severity escalation. A diff containing
// any of these gets severity=critical regardless of the operator's
// notify_threshold.
var aideCriticalPaths = []string{
	"/etc/passwd",
	"/etc/shadow",
	"/etc/sudoers",
	"/usr/local/bin/jabali-",
	"/root/.ssh/authorized_keys",
}

// runAideTamper polls security.aide.status. Fires
// aide.tamper.detected when the latest report shows added/changed/
// removed > 0. M42 (ADR-0087).
func runAideTamper(ctx context.Context, d Deps) {
	if d.Agent == nil || d.Queue == nil {
		d.Log.Info("eventsources: aide.tamper.detected disabled (Agent or Queue nil)")
		return
	}
	d.Log.Info("eventsources: aide.tamper.detected started", "tick", aideTamperTick.String())
	aideTamperPass(ctx, d)
	tick := time.NewTicker(aideTamperTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		aideTamperPass(ctx, d)
	}
}

func aideTamperPass(ctx context.Context, d Deps) {
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := d.Agent.Call(callCtx, "security.aide.status", map[string]any{})
	if err != nil {
		d.Log.Debug("eventsources: aide.status agent call failed", "err", err)
		return
	}
	var st struct {
		Enabled     bool   `json:"enabled"`
		LastCheckTS string `json:"last_check_ts"`
		Summary     struct {
			Added   int `json:"added"`
			Changed int `json:"changed"`
			Removed int `json:"removed"`
		} `json:"summary"`
		Sample []struct {
			Path       string `json:"path"`
			ChangeType string `json:"change_type"`
		} `json:"sample"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		d.Log.Warn("eventsources: aide.status parse failed", "err", err)
		return
	}
	if !st.Enabled {
		return
	}
	total := st.Summary.Added + st.Summary.Changed + st.Summary.Removed
	if total == 0 {
		return
	}

	// Dedupe via the timestamp of the report — same report-ts means
	// same check run, fire only once.
	dedupe := "aide-ts=" + st.LastCheckTS
	if !shouldFire(ctx, d, "aide.tamper.detected", dedupe, aideTamperCooldown) {
		return
	}

	severity := classifyAideSeverity(st.Sample)

	title := fmt.Sprintf("File integrity tamper detected (%d added / %d changed / %d removed)",
		st.Summary.Added, st.Summary.Changed, st.Summary.Removed)
	body := dedupe + "\n\n"
	for i, row := range st.Sample {
		if i >= 10 {
			body += fmt.Sprintf("...+%d more\n", len(st.Sample)-10)
			break
		}
		body += fmt.Sprintf("%s  %s\n", row.ChangeType, row.Path)
	}
	if _, err := d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: "aide.tamper.detected",
		Severity:  severity,
		Title:     title,
		Body:      body,
		Deeplink:  "/jabali-admin/security?tab=aide",
	}); err != nil {
		d.Log.Warn("eventsources: aide.tamper.detected publish failed", "err", err)
	}
}

func classifyAideSeverity(sample []struct {
	Path       string `json:"path"`
	ChangeType string `json:"change_type"`
}) string {
	for _, row := range sample {
		for _, crit := range aideCriticalPaths {
			if len(row.Path) >= len(crit) && row.Path[:len(crit)] == crit {
				return "critical"
			}
		}
	}
	return "warning"
}
