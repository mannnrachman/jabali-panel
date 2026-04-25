package eventsources

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

const (
	loadHighTick    = 5 * time.Minute
	loadHighCoolOff = 30 * time.Minute
	// Threshold expressed as load1 / NumCPU. 1.5 means a sustained
	// >150% utilisation across all cores — well past "noticeable" but
	// short of "doomed", giving the operator a chance to react.
	loadHighRatio = 1.5
)

// runLoadHigh polls /proc/loadavg every 5 minutes and fires a
// `load.high` envelope when the 1-minute load average exceeds
// loadHighRatio × runtime.NumCPU. 30-minute cooldown so a long
// sustained spike doesn't flood the inbox.
func runLoadHigh(ctx context.Context, d Deps) {
	tick := time.NewTicker(loadHighTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		loadHighPass(ctx, d)
	}
}

func loadHighPass(ctx context.Context, d Deps) {
	load1, err := readLoad1()
	if err != nil {
		// Probably not Linux (dev box). Quiet — this source is
		// best-effort.
		return
	}
	cpus := runtime.NumCPU()
	if cpus < 1 {
		cpus = 1
	}
	threshold := float64(cpus) * loadHighRatio
	if load1 < threshold {
		return
	}
	tag := fmt.Sprintf("load1=%.2f cpus=%d", load1, cpus)
	if !shouldFire(ctx, d, "load.high", tag, loadHighCoolOff) {
		return
	}
	_, err = d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: "load.high",
		Severity:  models.NotificationSeverityWarning,
		Title:     fmt.Sprintf("High server load: %.2f (threshold %.2f)", load1, threshold),
		Body:      fmt.Sprintf("1-minute load average %.2f on %d-core host. (%s)", load1, cpus, tag),
		Deeplink:  "/jabali-admin/dashboard",
	})
	if err != nil {
		d.Log.Warn("eventsources: publish load.high failed", "err", err)
	}
}

// readLoad1 parses /proc/loadavg and returns the first column. Linux
// only; non-Linux callers get an error and the source no-ops.
func readLoad1() (float64, error) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	parts := strings.Fields(string(b))
	if len(parts) < 1 {
		return 0, fmt.Errorf("malformed /proc/loadavg")
	}
	return strconv.ParseFloat(parts[0], 64)
}
