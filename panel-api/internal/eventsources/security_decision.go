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
	securityDecisionTick    = 5 * time.Minute
	securityDecisionCoolOff = 30 * time.Minute
)

// runSecurityDecision aggregates "who dropped this packet" signals
// across the three M43 decision brains (UFW, nginx limit_req,
// CrowdSec) into a single security.decision.fired event source. M43
// Step 2 — answers "who dropped this?" without the operator grepping
// 5 logs.
//
// Today the source is journalctl-driven for UFW + nginx limit_req
// (low-rate signal). CrowdSec has its own crowdsec.ban.spike source
// already; this one stays scoped to the gaps. Counts are summed per
// tick and fired only when the total > 0 — every M14 envelope
// represents real activity, not heartbeat noise.
//
// Disabled silently when journalctl isn't on PATH (minimal dev box).
func runSecurityDecision(ctx context.Context, d Deps) {
	bin, _ := exec.LookPath("journalctl")
	if bin == "" {
		d.Log.Info("eventsources: security_decision disabled (journalctl not found)")
		return
	}
	tick := time.NewTicker(securityDecisionTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		securityDecisionPass(ctx, d, bin)
	}
}

func securityDecisionPass(ctx context.Context, d Deps, bin string) {
	since := d.Now().Add(-securityDecisionTick).UTC().Format("2006-01-02 15:04:05")

	ufwDrops := journalctlGrepCount(ctx, bin, since, "UFW BLOCK")
	nginxRateLimits := journalctlGrepCount(ctx, bin, since, "limiting requests")

	total := ufwDrops + nginxRateLimits
	if total == 0 {
		return
	}

	// Dedupe key includes the *bucket* of activity so a sustained burst
	// fires once and a fresh spike fires again.
	tag := fmt.Sprintf("ufw=%d,limitreq=%d", ufwDrops, nginxRateLimits)
	if !shouldFire(ctx, d, "security.decision.fired", tag, securityDecisionCoolOff) {
		return
	}

	severity := models.NotificationSeverityInfo
	if total >= 100 {
		severity = models.NotificationSeverityWarning
	}

	body := fmt.Sprintf(
		"In the last %s: UFW drops=%d, nginx limit_req throttles=%d. (CrowdSec bans tracked separately via crowdsec.ban.spike.) See Security → Trust tab.",
		securityDecisionTick, ufwDrops, nginxRateLimits,
	)

	if _, err := d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: "security.decision.fired",
		Severity:  severity,
		Title:     fmt.Sprintf("Security: %d decision(s) fired", total),
		Body:      body,
		Deeplink:  "/jabali-admin/security?tab=trust",
	}); err != nil {
		d.Log.Warn("eventsources: publish security.decision.fired failed", "err", err)
	}
}

// journalctlGrepCount runs `journalctl --since "<since>" --no-pager` and
// counts lines containing needle. Capped at 10 000 lines to avoid
// blowing memory on a runaway log.
func journalctlGrepCount(ctx context.Context, bin, since, needle string) int {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "--since", since, "--no-pager", "-q")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0
	}
	if err := cmd.Start(); err != nil {
		return 0
	}
	defer func() { _ = cmd.Wait() }()

	count := 0
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	const maxLines = 10000
	scanned := 0
	for scanner.Scan() {
		if scanned > maxLines {
			break
		}
		scanned++
		if strings.Contains(scanner.Text(), needle) {
			count++
		}
	}
	return count
}
