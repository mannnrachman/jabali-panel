package eventsources

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

// runSSHLogin tails the systemd journal for sshd Accepted lines and
// publishes one ssh.login envelope per successful authentication.
//
// We shell out to journalctl --follow rather than linking against
// libsystemd's sd_journal: the panel-api binary already statics-builds
// to ship the same artefact across Debian/Ubuntu/Rocky, and pulling in
// CGo for sdjournal would break that. journalctl is part of systemd
// itself, so it's available wherever the dispatcher runs.
//
// JSON output gives us _COMM, _SYSTEMD_UNIT, MESSAGE, and __REALTIME_TIMESTAMP
// without needing to parse the human-readable log line. Successful auth
// lines look like:
//
//	Apr 25 21:10:00 host sshd[1234]: Accepted publickey for alice from 10.0.0.5 port 49152 ssh2: ED25519 SHA256:...
//
// Failed-auth lines start with "Failed" — we ignore them; CrowdSec
// already drives bans on those.
func runSSHLogin(ctx context.Context, d Deps) {
	// `command -v` would be cleaner but we prefer a direct lookup so
	// the warning logs include the missing binary name.
	if _, err := exec.LookPath("journalctl"); err != nil {
		d.Log.Debug("eventsources: ssh_login disabled — journalctl not in PATH")
		return
	}

	// Keep retrying with a backoff so a transient journalctl exit
	// (rotated journal, OOM kill) doesn't permanently silence ssh
	// notifications. Cap the backoff so we don't drift toward minute-
	// long gaps after a long incident.
	backoff := 2 * time.Second
	const maxBackoff = 60 * time.Second

	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := tailSSHJournal(ctx, d); err != nil && ctx.Err() == nil {
			d.Log.Warn("eventsources: ssh_login journal tail exited", "err", err, "retry_in", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		// Clean exit (ctx cancelled) — don't restart.
		return
	}
}

// tailSSHJournal runs journalctl in JSON-follow mode until ctx is
// cancelled or the process exits. Each Accepted line is parsed and
// published as ssh.login.
func tailSSHJournal(ctx context.Context, d Deps) error {
	cmd := exec.CommandContext(ctx,
		"journalctl",
		"--unit=ssh.service",
		"--unit=sshd.service",
		"--identifier=sshd",
		"--output=json",
		"--follow",
		"--since=now",
		"--no-pager",
		"-n", "0", // skip historical lines, only follow forward
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdoutpipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start journalctl: %w", err)
	}

	sc := bufio.NewScanner(stdout)
	// Default 64KB buffer can clip very long auth log lines (CIDR
	// allowlists in MOTDs, etc.); bump to 1MB.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var entry journalEntry
		if err := json.Unmarshal(sc.Bytes(), &entry); err != nil {
			continue
		}
		processSSHJournalEntry(ctx, d, entry)
	}
	// Wait so we collect the exit error if the scanner ended on a
	// process death rather than ctx cancellation.
	waitErr := cmd.Wait()
	if scanErr := sc.Err(); scanErr != nil && ctx.Err() == nil {
		return fmt.Errorf("scan: %w", scanErr)
	}
	if waitErr != nil && ctx.Err() == nil {
		return fmt.Errorf("journalctl exit: %w", waitErr)
	}
	return nil
}

// journalEntry captures the journalctl --output=json fields we care
// about. Other fields (priority, hostname, …) are ignored.
type journalEntry struct {
	Comm    string `json:"_COMM"`
	Message string `json:"MESSAGE"`
}

// processSSHJournalEntry decodes a single journal record and fires a
// ssh.login envelope for matching Accepted lines.
//
// Modern OpenSSH on Debian 13+ logs auth events from the per-session
// worker (`_COMM=sshd-session`) rather than the listener; older Debians
// still use `sshd`. Accept both, plus any sshd-* prefix to cover the
// privsep variants without listing every name explicitly.
func processSSHJournalEntry(ctx context.Context, d Deps, entry journalEntry) {
	if entry.Comm != "sshd" && !strings.HasPrefix(entry.Comm, "sshd-") {
		return
	}
	if !strings.HasPrefix(entry.Message, "Accepted ") {
		return
	}
	user, ip, method := parseAcceptedLine(entry.Message)
	if user == "" {
		return
	}
	tag := fmt.Sprintf("ssh:%s@%s", user, ip)
	// 30 second cooldown — repeat logins from the same user+IP collapse
	// (rsync/scp loops, CI bots) but distinct sessions still fire.
	if !shouldFire(ctx, d, "ssh.login", tag, 30*time.Second) {
		return
	}
	body := fmt.Sprintf("User %s logged in over SSH from %s", user, ip)
	if method != "" {
		body += fmt.Sprintf(" via %s", method)
	}
	body += fmt.Sprintf(". (%s)", tag)
	_, err := d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: "ssh.login",
		Severity:  models.NotificationSeverityInfo,
		Title:     fmt.Sprintf("SSH login: %s from %s", user, ip),
		Body:      body,
		Deeplink:  "/jabali-admin/security",
	})
	if err != nil {
		d.Log.Warn("eventsources: publish ssh.login failed", "err", err)
	}
}

// parseAcceptedLine pulls user/ip/method out of an OpenSSH "Accepted"
// log line. Returns empty strings if the line shape doesn't match.
//
// Real-world format:
//
//	Accepted publickey for alice from 10.0.0.5 port 49152 ssh2: ED25519 SHA256:...
//	Accepted password for bob from 192.0.2.1 port 51010 ssh2
//
// Missing fields bail out quietly — the dispatcher prefers a quiet
// gap to a malformed envelope.
func parseAcceptedLine(msg string) (user, ip, method string) {
	fields := strings.Fields(msg)
	// Need at least: Accepted <method> for <user> from <ip> ...
	if len(fields) < 6 {
		return "", "", ""
	}
	if fields[0] != "Accepted" || fields[2] != "for" || fields[4] != "from" {
		return "", "", ""
	}
	return fields[3], fields[5], fields[1]
}
