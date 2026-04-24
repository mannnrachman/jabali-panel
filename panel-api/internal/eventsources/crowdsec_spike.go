package eventsources

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

const (
	crowdSecTick       = 5 * time.Minute
	crowdSecThreshold  = 10
	crowdSecCoolOff    = 15 * time.Minute
)

// runCrowdSec polls `cscli decisions list -o json` every 5 minutes and
// fires crowdsec.ban.spike when the active-ban count exceeds the
// threshold. Disabled automatically when the binary isn't on PATH
// (CrowdSec is an optional install on a minimal dev box).
//
// We don't care about per-IP detail — the envelope gives the count and
// a pointer to the admin Security tab; an operator who wants specifics
// can dive in from there.
func runCrowdSec(ctx context.Context, d Deps) {
	bin := d.CrowdSecBin
	if bin == "" {
		bin, _ = exec.LookPath("cscli")
	}
	if bin == "" {
		d.Log.Info("eventsources: crowdsec_spike disabled (cscli not found)")
		return
	}
	crowdSecPass(ctx, d, bin)
	tick := time.NewTicker(crowdSecTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		crowdSecPass(ctx, d, bin)
	}
}

func crowdSecPass(ctx context.Context, d Deps, bin string) {
	n, err := crowdSecActiveDecisions(ctx, bin)
	if err != nil {
		d.Log.Debug("eventsources: cscli query failed", "err", err)
		return
	}
	if n < crowdSecThreshold {
		return
	}
	tag := fmt.Sprintf("decisions>=%d", crowdSecThreshold)
	if !shouldFire(ctx, d, "crowdsec.ban.spike", tag, crowdSecCoolOff) {
		return
	}
	_, err = d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: "crowdsec.ban.spike",
		Severity:  models.NotificationSeverityWarning,
		Title:     fmt.Sprintf("CrowdSec: %d active bans", n),
		Body:      fmt.Sprintf("CrowdSec reports %d active decisions. (%s)", n, tag),
		Deeplink:  "/admin/security",
	})
	if err != nil {
		d.Log.Warn("eventsources: publish crowdsec spike failed", "err", err)
	}
}

// crowdSecActiveDecisions shells out to `cscli decisions list -o json`
// and counts the top-level array length. We prefer -o json to `count`
// because the count sub-command isn't present on older CrowdSec
// releases (1.4 vs 1.6) and the JSON path is stable across both.
func crowdSecActiveDecisions(ctx context.Context, bin string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "decisions", "list", "-o", "json").Output()
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return 0, nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}
