package eventsources

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

const (
	systemUpdateTick    = 6 * time.Hour
	systemUpdateCoolOff = 24 * time.Hour
)

// runSystemUpdate polls `apt list --upgradable` every 6 hours and
// fires `system.update.available` (info) when one or more upgrades
// are pending. 24-hour cooldown so the operator only gets one
// reminder per day even if the cron'd `apt update` keeps populating
// the cache. apt list runs as the panel service user; no sudo
// required for the read-only listing.
func runSystemUpdate(ctx context.Context, d Deps) {
	// One-shot at boot so a stuck pending pile gets surfaced
	// immediately rather than 6h later.
	systemUpdatePass(ctx, d)
	tick := time.NewTicker(systemUpdateTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		systemUpdatePass(ctx, d)
	}
}

func systemUpdatePass(ctx context.Context, d Deps) {
	count, security, err := countUpgradablePackages(ctx)
	if err != nil {
		// Probably non-Debian or apt missing — quiet by design.
		return
	}
	if count == 0 {
		return
	}
	tag := fmt.Sprintf("count=%d security=%d", count, security)
	if !shouldFire(ctx, d, "system.update.available", tag, systemUpdateCoolOff) {
		return
	}
	severity := models.NotificationSeverityInfo
	if security > 0 {
		severity = models.NotificationSeverityWarning
	}
	title := fmt.Sprintf("%d package update(s) available", count)
	if security > 0 {
		title = fmt.Sprintf("%d update(s) — %d security", count, security)
	}
	_, err = d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: "system.update.available",
		Severity:  severity,
		Title:     title,
		Body:      fmt.Sprintf("Run `apt list --upgradable` for the full list. (%s)", tag),
		Deeplink:  "/jabali-admin/dashboard",
	})
	if err != nil {
		d.Log.Warn("eventsources: publish system.update.available failed", "err", err)
	}
}

// countUpgradablePackages runs `apt list --upgradable` and counts
// rows. apt prints "Listing..." on stderr (suppressed by -qq) and
// one line per upgradable package on stdout, formatted:
//   <name>/<archive> <newver> <arch> [upgradable from: <oldver>]
// security archive lines contain "-security" in the archive token.
func countUpgradablePackages(ctx context.Context) (total int, security int, err error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "apt", "list", "--upgradable", "-qq")
	cmd.Env = append(cmd.Env, "LANG=C", "LC_ALL=C")
	stdout, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(stdout)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Skip apt's banner line (starts with "Listing...") even
		// though -qq usually suppresses it.
		if strings.HasPrefix(line, "Listing") {
			continue
		}
		total++
		// Parse "<name>/<archive> ..." — security iff archive has
		// "-security" suffix (e.g. trixie-security).
		if idx := strings.Index(line, "/"); idx > 0 {
			rest := line[idx+1:]
			if sp := strings.Index(rest, " "); sp > 0 {
				archive := rest[:sp]
				if strings.Contains(archive, "-security") {
					security++
				}
			}
		}
	}
	return total, security, nil
}
