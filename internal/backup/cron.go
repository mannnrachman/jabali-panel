// Package backup — cron parsing for the M30.1 scheduler. Standard
// 5-field cron (no seconds, no @yearly etc.) keeps the UI simple and
// matches the presets emitted by the admin form (Daily/Weekly/Monthly).
//
// Errors here are surfaced to the REST validator so the admin sees a
// clean "invalid cron expression" rather than a 500.
package backup

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// CronParser is the package-level parser. Standard parser = minute,
// hour, day-of-month, month, day-of-week. No seconds, no descriptors.
var CronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// PresetCronExpr maps the UI preset names to canonical cron strings.
// "custom" returns the empty string — caller must look at the raw
// cron_expr field instead.
var PresetCronExpr = map[string]string{
	"daily":   "0 3 * * *",   // every day at 03:00
	"weekly":  "0 3 * * 0",   // Sunday at 03:00
	"monthly": "0 3 1 * *",   // 1st of month at 03:00
}

// ParseCron returns a robfig schedule or a wrapped error.
func ParseCron(expr string) (cron.Schedule, error) {
	sched, err := CronParser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return sched, nil
}

// NextFire computes the next firing strictly after `from`. The robfig
// parser already enforces the "strictly after" semantics so this is a
// thin wrapper kept for call-site clarity.
func NextFire(expr string, from time.Time) (time.Time, error) {
	sched, err := ParseCron(expr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(from), nil
}

// PreviewFires returns the next n firings strictly after `from`. The UI
// uses this for the "next 5 firings" preview on the schedule drawer.
// Bounded by n; callers cap at a sane number (5-10).
func PreviewFires(expr string, from time.Time, n int) ([]time.Time, error) {
	if n <= 0 {
		return nil, nil
	}
	sched, err := ParseCron(expr)
	if err != nil {
		return nil, err
	}
	out := make([]time.Time, 0, n)
	cursor := from
	for i := 0; i < n; i++ {
		next := sched.Next(cursor)
		out = append(out, next)
		cursor = next
	}
	return out, nil
}
