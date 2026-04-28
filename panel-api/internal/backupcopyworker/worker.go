// Package backupcopyworker — M30.1 in-process copy worker. Ticks every
// 60s, scans backup_copy_jobs.status=queued, spawns systemd-run
// transient units to run `restic copy` per row.
//
// systemd-run carry-over from M29: a backup_copy that survives a panel
// restart must outlive the panel-api process. Each copy = one transient
// service `jabali-backup-copy-<copy-job-id>.service` with the same
// hardening as the M30 retention timer (PrivateTmp, ProtectSystem=strict,
// ProtectHome=read-only, ReadWritePaths=/var/lib/jabali-backups + the
// dest's RESTIC_CACHE_DIR).
//
// The transient unit runs `jabali backup copy run --copy-job-id=<id>`,
// which loads the destination + creds env file and shells out to
// restic copy. The worker only spawns + bookkeeps; the heavy lifting
// is in the CLI.
package backupcopyworker

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// TickInterval is the polling cadence for the queue scanner.
const TickInterval = 60 * time.Second

// MaxConcurrentCopies caps how many copy jobs we'll launch per tick.
// Backups can be GB-scale; running 50 in parallel saturates the network
// link. Tunable later via server_settings if real demand surfaces.
const MaxConcurrentCopies = 5

// Backoff schedule for failed copies (M30.1 plan §"Risk + rollout").
var Backoff = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	30 * time.Minute,
}

type Deps struct {
	CopyJobs     repository.BackupCopyJobRepository
	Destinations repository.BackupDestinationRepository
	Log          *slog.Logger
}

type Worker struct{ deps Deps }

func New(deps Deps) *Worker {
	if deps.CopyJobs == nil || deps.Destinations == nil || deps.Log == nil {
		return nil
	}
	return &Worker{deps: deps}
}

func (w *Worker) Start(ctx context.Context) {
	t := time.NewTicker(TickInterval)
	defer t.Stop()
	w.deps.Log.Info("backup copy worker started", "tick_interval", TickInterval)
	w.tickOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			w.deps.Log.Info("backup copy worker stopped")
			return
		case <-t.C:
			w.tickOnce(ctx)
		}
	}
}

func (w *Worker) tickOnce(ctx context.Context) {
	now := time.Now().UTC()
	queued, err := w.deps.CopyJobs.ListQueued(ctx, now, MaxConcurrentCopies)
	if err != nil {
		w.deps.Log.Error("copy-worker list-queued failed", "err", err)
		return
	}
	for _, cj := range queued {
		w.spawn(ctx, cj)
	}
}

// spawn launches one systemd-run transient unit per copy job. Failures
// to spawn flip the row back to `failed` immediately (with retry if
// under cap); successful spawn flips to `running` and the unit takes
// over from there.
func (w *Worker) spawn(ctx context.Context, cj models.BackupCopyJob) {
	logger := w.deps.Log.With(
		"copy_job_id", cj.ID,
		"backup_job_id", cj.BackupJobID,
		"destination_id", cj.DestinationID,
	)

	dest, err := w.deps.Destinations.Get(ctx, cj.DestinationID)
	if err != nil {
		logger.Error("destination lookup failed", "err", err)
		w.fail(ctx, cj, fmt.Sprintf("destination lookup: %v", err))
		return
	}
	if !dest.Enabled {
		logger.Info("destination disabled; cancelling copy", "destination", dest.Name)
		_ = w.deps.CopyJobs.Cancel(ctx, cj.ID)
		return
	}
	if dest.Kind == models.BackupDestinationKindLocal {
		// Copy to local = no-op. Mark succeeded immediately.
		_ = w.deps.CopyJobs.MarkSucceeded(ctx, cj.ID, 0)
		return
	}

	unitName := fmt.Sprintf("jabali-backup-copy-%s.service", cj.ID)
	args := []string{
		"--unit=" + unitName,
		"--collect",
		"--quiet",
		// Hardening (matches retention timer; details in
		// install/systemd/jabali-backup-retention.service):
		"--property=PrivateTmp=yes",
		"--property=ProtectSystem=strict",
		"--property=ProtectHome=read-only",
		"--property=ProtectKernelTunables=yes",
		"--property=ProtectKernelModules=yes",
		"--property=ProtectControlGroups=yes",
		"--property=NoNewPrivileges=yes",
		"--property=ReadWritePaths=/var/lib/jabali-backups",
		"--property=Environment=HOME=/root",
		"--property=Environment=RESTIC_CACHE_DIR=/var/lib/jabali-backups/.cache/restic",
		"/usr/local/bin/jabali",
		"backup", "copy", "run",
		"--copy-job-id", cj.ID,
	}
	cmd := exec.CommandContext(ctx, "systemd-run", args...) //nolint:gosec // arg list constructed from constants + DB ULIDs
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Error("systemd-run spawn failed", "err", err, "output", string(out))
		w.fail(ctx, cj, fmt.Sprintf("systemd-run: %v: %s", err, string(out)))
		return
	}

	if err := w.deps.CopyJobs.MarkRunning(ctx, cj.ID, unitName); err != nil {
		logger.Error("mark-running failed", "err", err)
		// Don't fail the row — the transient unit is already alive.
		// The CLI inside the unit will write the terminal status.
	}
}

// fail marks the row failed-with-retry if under the attempt cap, or
// terminally failed otherwise. The retry path computes nextAttemptAt
// from the Backoff schedule indexed by the current retry count.
func (w *Worker) fail(ctx context.Context, cj models.BackupCopyJob, errText string) {
	attempt := cj.RetryCount + 1
	if attempt < models.BackupCopyJobMaxAttempts && attempt-1 < len(Backoff) {
		next := time.Now().UTC().Add(Backoff[attempt-1])
		_ = w.deps.CopyJobs.MarkFailed(ctx, cj.ID, errText, true, &next)
		return
	}
	_ = w.deps.CopyJobs.MarkFailed(ctx, cj.ID, errText, false, nil)
}
