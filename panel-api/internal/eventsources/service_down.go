package eventsources

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

const (
	serviceDownTick    = time.Minute
	serviceDownCoolOff = 5 * time.Minute
)

// jabaliUnits is the hard-coded list of systemd units we monitor. It
// matches what install.sh enables — add new units here when
// install.sh adds them.
var jabaliUnits = []string{
	"jabali-panel.service",
	"jabali-agent.service",
	"jabali-kratos.service",
	"jabali-bulwark.service",
	"jabali-stalwart.service",
	"jabali-pdns.service",
	"jabali-pdns-recursor.service",
}

// runServiceDown polls systemctl is-active for each configured
// jabali-* unit every minute. A non-active result fires service.down.
// Dedupe is per-unit: the event only re-fires every 5 minutes so a
// genuinely broken service doesn't spam the queue, but a flap on one
// unit doesn't silence a separate failure on another.
//
// We exec systemctl rather than talking D-Bus directly to stay aligned
// with ADR-0050 (panel-api has no privileged daemon bus access).
func runServiceDown(ctx context.Context, d Deps) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		d.Log.Info("eventsources: service_down disabled (systemctl not found)")
		return
	}
	serviceDownPass(ctx, d)
	tick := time.NewTicker(serviceDownTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		serviceDownPass(ctx, d)
	}
}

func serviceDownPass(ctx context.Context, d Deps) {
	for _, unit := range jabaliUnits {
		state, err := unitState(ctx, unit)
		if err != nil {
			// systemctl returns non-zero for inactive/failed — that's
			// the signal we want, not an actual error. Only warn for
			// true execution failures (binary missing, etc.).
			d.Log.Debug("eventsources: service_down lookup failed", "unit", unit, "err", err)
		}
		if state == "active" || state == "activating" {
			continue
		}
		// "inactive", "failed", "deactivating", "reloading" all fire.
		// An enabled-but-not-loaded unit ("not-found") fires too — a
		// missing unit file after an upgrade is itself a service-down.
		fireServiceDown(ctx, d, unit, state)
	}
}

func fireServiceDown(ctx context.Context, d Deps, unit, state string) {
	tag := "unit:" + unit
	if !shouldFire(ctx, d, "service.down", tag, serviceDownCoolOff) {
		return
	}
	_, err := d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: "service.down",
		Severity:  models.NotificationSeverityError,
		Title:     fmt.Sprintf("%s is %s", unit, state),
		Body:      fmt.Sprintf("systemd reports %s for %s. Run `journalctl -u %s` for details. (%s)", state, unit, unit, tag),
		Deeplink:  "/admin/system",
	})
	if err != nil {
		d.Log.Warn("eventsources: publish service_down failed", "unit", unit, "err", err)
	}
}

func unitState(ctx context.Context, unit string) (string, error) {
	// `systemctl is-active <unit>` prints the state + exit 0 (active)
	// or non-zero (everything else). We want the string either way.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "is-active", unit).Output()
	state := strings.TrimSpace(string(out))
	return state, err
}
