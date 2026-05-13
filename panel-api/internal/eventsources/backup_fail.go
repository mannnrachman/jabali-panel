package eventsources

// M14 backup_fail event source.
//
// Polls backup_jobs for status='failed' rows whose finished_at lies
// within the last lookback window. Fires one `backup.fail` envelope
// per freshly-failed job, deduped via the History layer on the
// job_id dedupe tag so a re-poll never duplicates an alert.
//
// Off when BackupJobs repo is nil — tests + minimal panel installs
// (no M30 backup stack wired) skip the source.

import (
	"context"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

const (
	backupFailTick     = 5 * time.Minute
	backupFailLookback = 30 * time.Minute
	backupFailCoolOff  = 24 * time.Hour
)

func runBackupFail(ctx context.Context, d Deps) {
	if d.BackupJobs == nil {
		d.Log.Debug("eventsources: backup_fail disabled (no BackupJobs repo)")
		return
	}
	// Immediate pass so a panel restart doesn't sleep through a fresh
	// failure that landed during the down window.
	backupFailPass(ctx, d)
	tick := time.NewTicker(backupFailTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		backupFailPass(ctx, d)
	}
}

func backupFailPass(ctx context.Context, d Deps) {
	since := d.Now().Add(-backupFailLookback)
	rows, err := d.BackupJobs.ListByStatusSince(ctx, models.BackupJobStatusFailed, since, 200)
	if err != nil {
		d.Log.Warn("eventsources: backup_fail list failed", "err", err)
		return
	}
	for _, j := range rows {
		dedupeTag := "backup_job_id=" + j.ID
		if !shouldFire(ctx, d, "backup.fail", dedupeTag, backupFailCoolOff) {
			continue
		}
		errTail := j.ErrorText
		if len(errTail) > 256 {
			errTail = errTail[len(errTail)-256:]
		}
		body := fmt.Sprintf(
			"Backup job %s (%s) failed. dedupe=%s\n%s",
			j.ID, j.Kind, dedupeTag, errTail,
		)
		title := fmt.Sprintf("Backup failed: %s", j.Kind)
		env := notifications.Envelope{
			EventKind: "backup.fail",
			Severity:  "critical",
			Title:     title,
			Body:      body,
			Deeplink:  "/jabali-admin/backups",
			UserID:    j.UserID,
		}
		if _, perr := d.Queue.Publish(ctx, env); perr != nil {
			d.Log.Warn("eventsources: backup_fail publish failed", "job_id", j.ID, "err", perr)
		}
	}
}
