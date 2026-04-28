// Package backupfinalizer — M30.1 in-process finalizer. Bridges the
// gap between "agent finished writing the manifest snapshot" and
// "panel-api marks the job succeeded + enqueues copy_jobs".
//
// Why a separate ticker instead of an agent → panel-api callback?
// The agent runs the orchestrator inline (M30 v1; v2 will move it to
// systemd-run + a real callback). Until then, panel-api polls the
// agent's backup.status which inspects restic for the manifest
// snapshot. Once a manifest is found, the job is considered done.
//
// Finalizer responsibilities:
//   1. List backup_jobs.status='running'.
//   2. For each, ask the agent if the manifest snapshot exists.
//   3. If yes -> mark succeeded + enqueue copy_jobs (if schedule_id != NULL).
//   4. If running >4h -> mark failed (safety timeout).
package backupfinalizer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

const (
	TickInterval     = 30 * time.Second
	StallTimeout     = 4 * time.Hour
	MaxJobsPerTick   = 25
)

type Deps struct {
	Jobs      repository.BackupJobRepository
	Schedules repository.BackupScheduleRepository
	CopyJobs  repository.BackupCopyJobRepository
	Agent     agent.AgentInterface
	Log       *slog.Logger
}

type Finalizer struct{ deps Deps }

func New(deps Deps) *Finalizer {
	if deps.Jobs == nil || deps.Schedules == nil || deps.CopyJobs == nil ||
		deps.Agent == nil || deps.Log == nil {
		return nil
	}
	return &Finalizer{deps: deps}
}

func (f *Finalizer) Start(ctx context.Context) {
	t := time.NewTicker(TickInterval)
	defer t.Stop()
	f.deps.Log.Info("backup finalizer started", "tick_interval", TickInterval)
	f.tickOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			f.deps.Log.Info("backup finalizer stopped")
			return
		case <-t.C:
			f.tickOnce(ctx)
		}
	}
}

// agentStatus mirrors backupStatusHandler's reply shape (panel-agent
// commands/backup_create.go). Kept here as a private struct because
// the agent package owns the canonical Go types and importing them
// from panel-agent would cross the boundary the wire is supposed to
// hide.
type agentStatus struct {
	JobID         string `json:"job_id"`
	Stages        []string `json:"stages"`
	ManifestFound bool   `json:"manifest_found"`
	Snapshots     []struct {
		ID   string   `json:"id"`
		Tags []string `json:"tags"`
	} `json:"snapshots"`
}

func (f *Finalizer) tickOnce(ctx context.Context) {
	rows, _, err := f.deps.Jobs.ListAll(ctx, MaxJobsPerTick, 0)
	if err != nil {
		f.deps.Log.Error("finalizer list-running failed", "err", err)
		return
	}
	now := time.Now().UTC()
	for _, j := range rows {
		if j.Status != models.BackupJobStatusRunning {
			continue
		}
		// Stall safety: a job that's been running >4h without a manifest
		// is broken — mark failed so retention can prune the half-baked
		// snapshots and the operator sees the error.
		if j.StartedAt != nil && now.Sub(*j.StartedAt) > StallTimeout {
			f.deps.Log.Warn("backup stalled past timeout; marking failed",
				"job_id", j.ID, "started_at", j.StartedAt)
			_ = f.deps.Jobs.MarkFinished(ctx, j.ID, models.BackupJobStatusFailed,
				"", "", 0, 0, nil, nil, "stalled: no manifest snapshot after 4h")
			continue
		}
		f.checkOne(ctx, j)
	}
}

func (f *Finalizer) checkOne(ctx context.Context, j models.BackupJob) {
	logger := f.deps.Log.With("job_id", j.ID)

	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := f.deps.Agent.Call(callCtx, "backup.status", map[string]string{"job_id": j.ID})
	if err != nil {
		logger.Debug("backup.status query failed; will retry", "err", err)
		return
	}
	var st agentStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		logger.Warn("malformed backup.status reply; skipping", "err", err)
		return
	}
	if !st.ManifestFound {
		return // still running
	}

	manifestSnapID := ""
	for _, s := range st.Snapshots {
		for _, tag := range s.Tags {
			if tag == "stage=manifest" {
				manifestSnapID = s.ID
				break
			}
		}
		if manifestSnapID != "" {
			break
		}
	}
	if err := f.deps.Jobs.MarkFinished(ctx, j.ID,
		models.BackupJobStatusSucceeded,
		manifestSnapID, "", 0, 0, nil, nil, ""); err != nil {
		logger.Error("mark succeeded failed", "err", err)
		return
	}
	logger.Info("backup finalized", "snapshot_id", manifestSnapID)

	if j.ScheduleID != nil && *j.ScheduleID != "" {
		f.enqueueCopies(ctx, j)
	}
}

// enqueueCopies fans out backup_copy_jobs rows for every destination
// linked to the schedule that fired this backup. Skips local kind
// (no-op copy). Idempotent: callers can re-enter without creating
// duplicates if they pre-check, but the finalizer guarantees one entry
// per check by transitioning the job to succeeded first.
func (f *Finalizer) enqueueCopies(ctx context.Context, j models.BackupJob) {
	logger := f.deps.Log.With("job_id", j.ID, "schedule_id", strDeref(j.ScheduleID))

	dests, err := f.deps.Schedules.GetDestinations(ctx, *j.ScheduleID)
	if err != nil {
		logger.Error("load schedule destinations failed", "err", err)
		return
	}
	for _, d := range dests {
		if !d.Enabled || d.Kind == models.BackupDestinationKindLocal {
			continue
		}
		cj := &models.BackupCopyJob{
			ID:            ids.NewULID(),
			BackupJobID:   j.ID,
			DestinationID: d.ID,
			Status:        models.BackupCopyJobStatusQueued,
		}
		if err := f.deps.CopyJobs.Create(ctx, cj); err != nil {
			logger.Error("enqueue copy_job failed",
				"destination_id", d.ID, "err", err)
			continue
		}
		logger.Info("copy_job enqueued",
			"copy_job_id", cj.ID, "destination", d.Name)
	}
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

// fmtErr keeps lints quiet when error wrapping isn't needed inline.
var _ = fmt.Sprintf
