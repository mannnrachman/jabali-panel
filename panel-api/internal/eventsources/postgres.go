package eventsources

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

// M37 PostgreSQL parity event source (ADR-0091).
//
// Polls postgres health every 2 minutes when server_settings.
// postgres_enabled is true. Three signals:
//   - service_down: postgresql.service is enabled but not active
//   - disk_high:    /var/lib/postgresql usage > 85%
//   - connections_exhausted: pg_stat_activity rows > 90% of
//                            max_connections
//
// Each signal cools off independently for 30 minutes after firing
// so a stuck-down database doesn't spam the operator.

const (
	postgresTick           = 2 * time.Minute
	postgresCoolOff        = 30 * time.Minute
	postgresDataDir        = "/var/lib/postgresql"
	postgresDiskHighPct    = 85.0
	postgresConnHighFactor = 0.90
)

func runPostgresMonitor(ctx context.Context, d Deps) {
	if d.ServerSettings == nil {
		// Without settings access we can't gate on postgres_enabled.
		// Bail rather than poll a definitely-disabled engine.
		d.Log.Debug("eventsources: postgres monitor skipped (no settings repo)")
		return
	}
	d.Log.Info("eventsources: postgres started", "tick", postgresTick.String())
	t := time.NewTicker(postgresTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pollPostgres(ctx, d)
		}
	}
}

func pollPostgres(ctx context.Context, d Deps) {
	settings, err := d.ServerSettings.Get(ctx)
	if err != nil || settings == nil || !settings.PostgresEnabled {
		return
	}

	// service_down: enabled but not active.
	if isUnitEnabled(ctx, "postgresql") && !isUnitActive(ctx, "postgresql") {
		if shouldFire(ctx, d, "postgres.service_down", "postgresql", postgresCoolOff) {
			publishPostgres(ctx, d, "postgres.service_down", "error",
				"PostgreSQL service down",
				"postgresql.service is enabled but not running. Connection attempts will fail.")
		}
		return // no point checking the rest if service is down
	}

	// disk_high: /var/lib/postgresql usage above threshold.
	if used, total, derr := pgDataDirUsage(); derr == nil && total > 0 {
		pct := float64(used) / float64(total) * 100.0
		if pct >= postgresDiskHighPct {
			if shouldFire(ctx, d, "postgres.disk_high", postgresDataDir, postgresCoolOff) {
				publishPostgres(ctx, d, "postgres.disk_high", "warning",
					fmt.Sprintf("PostgreSQL data dir at %.0f%% full", pct),
					fmt.Sprintf("%s usage %.1f%% of %d bytes — autovacuum + WAL retention may be at risk.",
						postgresDataDir, pct, total))
			}
		}
	}

	// connections_exhausted: pg_stat_activity vs max_connections.
	if active, max, cerr := pgConnectionStats(ctx); cerr == nil && max > 0 {
		ratio := float64(active) / float64(max)
		if ratio >= postgresConnHighFactor {
			if shouldFire(ctx, d, "postgres.connections_exhausted", "global", postgresCoolOff) {
				publishPostgres(ctx, d, "postgres.connections_exhausted", "error",
					fmt.Sprintf("PostgreSQL connections at %d/%d", active, max),
					"Active connection count above 90% of max_connections. New clients will be refused.")
			}
		}
	}
}

func publishPostgres(ctx context.Context, d Deps, kind, severity, title, body string) {
	_, err := d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: kind,
		Severity:  severity,
		Title:     title,
		Body:      body,
		Deeplink:  "/admin/server-status",
	})
	if err != nil {
		d.Log.Warn("eventsources: postgres publish failed", "kind", kind, "err", err)
	}
}

func isUnitActive(ctx context.Context, unit string) bool {
	out, _ := exec.CommandContext(ctx, "systemctl", "is-active", unit).Output()
	return strings.TrimSpace(string(out)) == "active"
}

func isUnitEnabled(ctx context.Context, unit string) bool {
	out, _ := exec.CommandContext(ctx, "systemctl", "is-enabled", unit).Output()
	state := strings.TrimSpace(string(out))
	return state == "enabled" || state == "alias" || state == "static"
}

func pgDataDirUsage() (used, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(postgresDataDir, &st); err != nil {
		return 0, 0, err
	}
	total = uint64(st.Blocks) * uint64(st.Bsize)
	avail := uint64(st.Bavail) * uint64(st.Bsize)
	used = total - avail
	return used, total, nil
}

func pgConnectionStats(ctx context.Context) (active, max int, err error) {
	out, err := exec.CommandContext(ctx, "sudo", "-u", "postgres", "psql",
		"-XAtq",
		"-c",
		`SELECT (SELECT count(*) FROM pg_stat_activity WHERE state IS NOT NULL),
                (SELECT setting::int FROM pg_settings WHERE name = 'max_connections')`).Output()
	if err != nil {
		return 0, 0, err
	}
	// Output: two rows on one tab-separated line, e.g. "5\t100".
	line := strings.TrimSpace(string(out))
	var a, m int
	if _, scanErr := fmt.Sscanf(line, "%d\t%d", &a, &m); scanErr != nil {
		// Older psql versions newline-separate; try that too.
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return 0, 0, fmt.Errorf("unparseable psql output %q", line)
		}
		fmt.Sscanf(parts[0], "%d", &a)
		fmt.Sscanf(parts[1], "%d", &m)
	}
	return a, m, nil
}
