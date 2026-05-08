// panel-api log-tail event source — surfaces ERROR-level slog lines
// from journalctl into the M14 notification dispatcher so admins
// see issues in the bell instead of having to tail journalctl.
//
// Polls every 2 minutes:
//   journalctl -u jabali-panel.service --since 2m -o json --no-pager
// classifies each line by msg keyword, buckets by (event_kind,
// fingerprint), and Publishes one envelope per bucket per 30-minute
// cooldown window. Cooldown reuses the standard shouldFire helper
// (history-row dedupe via body substring).
//
// Event-kind taxonomy:
//   - agent: internal:                → agent.dispatch.failure
//   - agent unreachable / sock missing → agent.unreachable
//   - reconcile/Reconciler error      → reconciler.error
//   - notifications: dispatch failed   → notifications.dlq.nonzero
//   - panel-api 5xx (rare in slog)    → panel.api.error
//
// Operator can disable any of the kinds in the Notifications →
// Events tab; the dispatcher honours per-kind toggles.
package eventsources

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

const (
	panelLogTailInterval = 2 * time.Minute
	panelLogTailLookback = 2 * time.Minute
	panelLogTailCooldown = 30 * time.Minute
)

func runPanelLogTail(ctx context.Context, d Deps) {
	if d.Queue == nil {
		return
	}
	d.Log.Info("eventsources: panel_log_tail started",
		"tick", panelLogTailInterval.String(),
		"lookback", panelLogTailLookback.String())

	t := time.NewTicker(panelLogTailInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			scanPanelLogs(ctx, d)
		}
	}
}

type journalLine struct {
	Message  string `json:"MESSAGE"`
	Priority string `json:"PRIORITY"`
}

// classifyError maps a slog-formatted ERROR line to one of the
// log-tail event kinds. Returns ("", "") if the line shouldn't
// emit a notification.
func classifyError(msg string) (kind, fingerprint string) {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "/run/jabali/agent.sock") &&
		(strings.Contains(low, "no such file") || strings.Contains(low, "connection refused")):
		return "agent.unreachable", "agent_sock_missing"

	case strings.Contains(low, "agent: internal:"):
		// Bucket by the first 80 chars of the post-"internal:" tail.
		idx := strings.Index(low, "agent: internal:")
		tail := strings.TrimSpace(msg[idx+len("agent: internal:"):])
		if len(tail) > 80 {
			tail = tail[:80]
		}
		return "agent.dispatch.failure", "agent_internal:" + tail

	case strings.Contains(low, "reconcile") && strings.Contains(low, "fail"):
		// "reconcile X failed" / "Y reconcile: ... failed"
		fp := firstNWords(msg, 6)
		return "reconciler.error", "reconcile:" + fp

	case strings.Contains(low, "notification dispatch failed") ||
		strings.Contains(low, "dlq") ||
		strings.Contains(low, "channel send failed"):
		return "notifications.dlq.nonzero", "dispatch_fail:" + firstNWords(msg, 4)

	case strings.Contains(low, "panel-api 5") ||
		strings.Contains(low, "http 5"):
		return "panel.api.error", "5xx:" + firstNWords(msg, 4)
	}
	return "", ""
}

func firstNWords(s string, n int) string {
	parts := strings.Fields(s)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, " ")
}

func scanPanelLogs(ctx context.Context, d Deps) {
	since := time.Now().Add(-panelLogTailLookback).Format("2006-01-02 15:04:05")
	cmd := exec.CommandContext(ctx, "journalctl",
		"-u", "jabali-panel.service",
		"--since", since,
		"-o", "json",
		"--no-pager",
		"-p", "err",
	)
	out, err := cmd.Output()
	if err != nil {
		// journalctl missing or permission denied — log once, skip.
		d.Log.Debug("eventsources: panel_log_tail journalctl failed", "err", err)
		return
	}

	type bucketKey struct {
		kind, fp string
	}
	bucket := map[bucketKey]string{}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var jl journalLine
		if err := json.Unmarshal([]byte(line), &jl); err != nil {
			continue
		}
		// slog-formatted MESSAGE is JSON; pull the inner "msg" + "err".
		var inner struct {
			Msg   string `json:"msg"`
			Err   string `json:"err"`
			Level string `json:"level"`
		}
		_ = json.Unmarshal([]byte(jl.Message), &inner)
		// Build a single haystack from msg+err so classify can match
		// either side. Fallback to the raw MESSAGE line for plain-text
		// log lines that aren't slog-JSON.
		haystack := jl.Message
		if inner.Msg != "" || inner.Err != "" {
			haystack = inner.Msg + " " + inner.Err
		}
		if inner.Level != "" && !strings.EqualFold(inner.Level, "error") &&
			!strings.EqualFold(inner.Level, "critical") {
			continue
		}
		kind, fp := classifyError(haystack)
		if kind == "" {
			continue
		}
		key := bucketKey{kind, fp}
		if _, ok := bucket[key]; !ok {
			bucket[key] = haystack
		}
	}

	for key, sample := range bucket {
		if !shouldFire(ctx, d, key.kind, key.fp, panelLogTailCooldown) {
			continue
		}
		title := titleFor(key.kind)
		body := truncateForBody(sample) + " (dedupe-key: " + key.fp + ")"
		_, err := d.Queue.Publish(ctx, notifications.Envelope{
			EventKind: key.kind,
			Severity:  severityFor(key.kind),
			Title:     title,
			Body:      body,
			Deeplink:  "/admin/server-status",
		})
		if err != nil {
			d.Log.Warn("eventsources: panel_log_tail publish failed",
				"event_kind", key.kind, "err", err)
		}
	}
}

func titleFor(kind string) string {
	switch kind {
	case "agent.dispatch.failure":
		return "Agent dispatch failure"
	case "agent.unreachable":
		return "Agent unreachable"
	case "reconciler.error":
		return "Reconciler error"
	case "notifications.dlq.nonzero":
		return "Notifications DLQ non-empty"
	case "panel.api.error":
		return "panel-api 5xx"
	}
	return kind
}

func severityFor(kind string) string {
	switch kind {
	case "agent.unreachable":
		return "error"
	default:
		return "warning"
	}
}

func truncateForBody(s string) string {
	const max = 480
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
