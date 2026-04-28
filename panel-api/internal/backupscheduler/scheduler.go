// Package backupscheduler — M30.1 in-process scheduler. Ticks every
// 60s, scans backup_schedules.next_run_at <= now, dispatches each
// overdue row to panel-agent via the same backup.create call the REST
// handler uses.
//
// Why in-process instead of a systemd timer + CLI tick (like the M30
// retention sweep)? At 60s cadence the cobra cold-start cost dominates,
// and we already have several goroutine-based tickers (reconciler, M14
// notification dispatcher, eventsources). Missed ticks during panel
// restart are tolerated — the schedules stay overdue and the next tick
// picks them up.
//
// One backup at a time per (kind, user-id) is enforced by the agent;
// the scheduler just dispatches and lets the agent return 409 Conflict
// when the prior job is still running. We log + skip on conflict.
package backupscheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	internalbackup "git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// TickInterval is the polling cadence. Bigger than 60s loses scheduling
// precision; smaller burns DB queries with no operator value.
const TickInterval = 60 * time.Second

// MaxDuePerTick caps how many overdue schedules one tick will dispatch.
// Bigger = potential DB contention with the agent's job-create lock;
// smaller = recovery from a long pause takes more ticks. 50 fits a
// 5-minute outage on a 1k-user host.
const MaxDuePerTick = 50

// Deps is the dependency bundle passed by serve.go.
type Deps struct {
	Schedules      repository.BackupScheduleRepository
	Jobs           repository.BackupJobRepository
	CopyJobs       repository.BackupCopyJobRepository
	Users          repository.UserRepository
	Databases      repository.DatabaseRepository
	DatabaseUsers  repository.DatabaseUserRepository
	DatabaseGrants repository.DatabaseUserGrantRepository
	Domains        repository.DomainRepository
	Mailboxes      repository.MailboxRepository
	AppInstalls    repository.ApplicationInstallRepository
	Agent          agent.AgentInterface
	Log            *slog.Logger
}

// Scheduler is the goroutine wrapper. Construct via New, start with
// Start(ctx). Returns immediately; the loop runs until ctx is done.
type Scheduler struct{ deps Deps }

// New returns a configured scheduler. Returns nil if any required dep
// is missing — callers log + skip start in that case so an incomplete
// deployment doesn't crash on boot.
func New(deps Deps) *Scheduler {
	if deps.Schedules == nil || deps.Jobs == nil || deps.CopyJobs == nil ||
		deps.Users == nil || deps.Agent == nil || deps.Log == nil {
		return nil
	}
	return &Scheduler{deps: deps}
}

// Start runs the tick loop until ctx is done.
func (s *Scheduler) Start(ctx context.Context) {
	t := time.NewTicker(TickInterval)
	defer t.Stop()
	s.deps.Log.Info("backup scheduler started", "tick_interval", TickInterval)
	// Run one tick immediately so a panel restart doesn't leave overdue
	// schedules waiting up to 60s for the next firing.
	s.tickOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			s.deps.Log.Info("backup scheduler stopped")
			return
		case <-t.C:
			s.tickOnce(ctx)
		}
	}
}

func (s *Scheduler) tickOnce(ctx context.Context) {
	now := time.Now().UTC()
	due, err := s.deps.Schedules.ListDue(ctx, now, MaxDuePerTick)
	if err != nil {
		s.deps.Log.Error("scheduler list-due failed", "err", err)
		return
	}
	for _, sched := range due {
		s.dispatch(ctx, sched)
	}
}

// dispatch handles one overdue schedule end-to-end: compute next firing,
// dispatch the backup, mark ran (or just bump next_run_at on conflict).
// Errors are logged but not returned — one bad row must not stall the
// whole tick.
func (s *Scheduler) dispatch(ctx context.Context, sched models.BackupSchedule) {
	logger := s.deps.Log.With(
		"schedule_id", sched.ID,
		"kind", sched.Kind,
		"user_id", strDeref(sched.UserID),
	)

	next, err := internalbackup.NextFire(sched.CronExpr, time.Now().UTC())
	if err != nil {
		logger.Error("invalid cron in DB; disabling for one tick", "cron_expr", sched.CronExpr, "err", err)
		// Push next_run_at far enough out that we don't re-process this
		// row every tick. Operator must fix the cron via UI.
		bump := time.Now().UTC().Add(24 * time.Hour)
		_ = s.deps.Schedules.UpdateNextRun(ctx, sched.ID, bump)
		return
	}

	dispatched, dispErr := s.dispatchBackup(ctx, sched)
	if dispErr != nil {
		logger.Error("scheduler dispatch failed", "err", dispErr)
		// Still advance next_run_at so we don't tight-loop on a
		// permanent failure (e.g. user deleted but FK didn't cascade).
		_ = s.deps.Schedules.UpdateNextRun(ctx, sched.ID, next)
		return
	}
	if !dispatched {
		// Conflict path (existing job running). Don't update last_run_at
		// since we didn't actually run, but advance next_run_at so the
		// next tick re-attempts on the proper cadence.
		_ = s.deps.Schedules.UpdateNextRun(ctx, sched.ID, next)
		return
	}

	if err := s.deps.Schedules.MarkRan(ctx, sched.ID, time.Now().UTC(), next); err != nil {
		logger.Error("schedule mark-ran failed", "err", err)
	}
}

// dispatchBackup creates the backup_jobs row, calls the agent, and
// returns true on success / false on conflict / non-nil error on
// terminal failure. The caller decides what to do with the schedule
// row based on the boolean.
func (s *Scheduler) dispatchBackup(ctx context.Context, sched models.BackupSchedule) (bool, error) {
	switch sched.Kind {
	case models.BackupScheduleKindAccount:
		// user_id NULL = fan out to every non-admin user. Each user
		// gets one backup_jobs row + one agent call. We dispatch
		// sequentially to keep the agent's per-user lock contention
		// predictable; the agent enforces one running backup per
		// user-id anyway. Returns true if at least one user was
		// dispatched (so the schedule still advances next_run_at and
		// records last_run_at).
		if sched.UserID == nil {
			notAdmin := false
			users, _, err := s.deps.Users.List(ctx, repository.ListOptions{
				Limit:   10000,
				IsAdmin: &notAdmin,
			})
			if err != nil {
				return false, fmt.Errorf("list users: %w", err)
			}
			any := false
			for i := range users {
				u := &users[i]
				if ok := s.runOneAccountBackup(ctx, sched, u); ok {
					any = true
				}
			}
			return any, nil
		}
		user, err := s.deps.Users.FindByID(ctx, *sched.UserID)
		if err != nil {
			return false, fmt.Errorf("load user %s: %w", *sched.UserID, err)
		}
		if user == nil {
			return false, fmt.Errorf("user %s not found", *sched.UserID)
		}
		if user.IsAdmin {
			return false, errors.New("account_backup schedule references admin user")
		}
		return s.runOneAccountBackup(ctx, sched, user), nil

	case models.BackupScheduleKindSystem:
		jobID := ids.NewULID()
		schedID := sched.ID
		job := &models.BackupJob{
			ID:         jobID,
			UserID:     "system",
			ScheduleID: &schedID,
			Kind:       models.BackupJobKindSystemBackup,
			Status:     models.BackupJobStatusQueued,
			CreatedAt:  time.Now().UTC(),
		}
		if err := s.deps.Jobs.Create(ctx, job); err != nil {
			return false, fmt.Errorf("create system backup_job: %w", err)
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		params := map[string]any{
			"job_id":           jobID,
			"include_accounts": false, // include_accounts toggle = manual-only in v1
		}
		if _, err := s.deps.Agent.Call(callCtx, "system.backup", params); err != nil {
			if isAgentConflict(err) {
				_ = s.deps.Jobs.MarkFinished(ctx, jobID, models.BackupJobStatusCancelled,
					"", "", 0, 0, nil, nil, "skipped: prior system backup still running")
				return false, nil
			}
			_ = s.deps.Jobs.MarkFinished(ctx, jobID, models.BackupJobStatusFailed,
				"", "", 0, 0, nil, nil, err.Error())
			return false, fmt.Errorf("agent system.backup: %w", err)
		}
		_ = s.deps.Jobs.MarkStarted(ctx, jobID)
		return true, nil

	default:
		return false, fmt.Errorf("unknown schedule kind %q", sched.Kind)
	}
}

// runOneAccountBackup creates one backup_jobs row + one backup.create
// agent call for the given user. Returns true on success or graceful
// conflict (existing job running), false on terminal error. All errors
// are logged and swallowed so a fan-out over many users never aborts
// after the first failure.
func (s *Scheduler) runOneAccountBackup(ctx context.Context, sched models.BackupSchedule, user *models.User) bool {
	logger := s.deps.Log.With("schedule_id", sched.ID, "user_id", user.ID)
	jobID := ids.NewULID()
	schedID := sched.ID
	job := &models.BackupJob{
		ID:         jobID,
		UserID:     user.ID,
		ScheduleID: &schedID,
		Kind:       models.BackupJobKindAccountBackup,
		Status:     models.BackupJobStatusQueued,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.deps.Jobs.Create(ctx, job); err != nil {
		logger.Error("create backup_job failed", "err", err)
		return false
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dbs := userDatabases(callCtx, s.deps, user.ID, logger)
	mbs := userMailboxes(callCtx, s.deps, user.ID, logger)
	meta := buildScheduleMetadata(callCtx, s.deps, user, logger)
	params := map[string]any{
		"job_id":    jobID,
		"user_id":   user.ID,
		"username":  user.Username,
		"email":     user.Email,
		"is_admin":  user.IsAdmin,
		"databases": dbs,
		"mailboxes": mbs,
		"metadata":  meta,
	}
	if _, err := s.deps.Agent.Call(callCtx, "backup.create", params); err != nil {
		if isAgentConflict(err) {
			_ = s.deps.Jobs.MarkFinished(ctx, jobID, models.BackupJobStatusCancelled,
				"", "", 0, 0, nil, nil, "skipped: prior backup still running")
			return true
		}
		_ = s.deps.Jobs.MarkFinished(ctx, jobID, models.BackupJobStatusFailed,
			"", "", 0, 0, nil, nil, err.Error())
		logger.Error("agent backup.create failed", "err", err)
		return false
	}
	_ = s.deps.Jobs.MarkStarted(ctx, jobID)
	return true
}

// isAgentConflict matches the agent's 409-equivalent NDJSON error string
// for "another job already running". Cheap heuristic — cleaner than a
// typed sentinel that would require coordinating both packages. False
// positives only delay one tick, no data loss.
func isAgentConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already running") ||
		strings.Contains(msg, "conflict") ||
		strings.Contains(msg, "job_already_active")
}

// userDatabases returns the names of every hosted database belonging
// to the user. Errors are logged + an empty slice returned so a
// transient DB hiccup in the scheduler doesn't abort the whole tick.
func userDatabases(ctx context.Context, deps Deps, userID string, logger *slog.Logger) []string {
	if deps.Databases == nil {
		return nil
	}
	dbs, _, err := deps.Databases.ListByUserID(ctx, userID, repository.ListOptions{Limit: 10000})
	if err != nil {
		logger.Warn("list databases for backup failed", "err", err)
		return nil
	}
	out := make([]string, 0, len(dbs))
	for _, d := range dbs {
		out = append(out, d.Name)
	}
	return out
}

// buildScheduleMetadata is the scheduler's mirror of
// BackupHandlerConfig.buildAccountMetadata. Returns a non-nil bundle
// even on partial repo failures so the agent's metadata stage stays
// consistent — missing pieces become empty arrays.
func buildScheduleMetadata(ctx context.Context, deps Deps, user *models.User, logger *slog.Logger) *internalbackup.AccountMetadata {
	m := &internalbackup.AccountMetadata{
		SchemaVersion: internalbackup.MetadataSchemaVersion,
		UserID:        user.ID,
		Email:         user.Email,
	}
	if user.Username != nil {
		m.Username = *user.Username
	}
	if deps.DatabaseUsers != nil && deps.Databases != nil {
		users, _, err := deps.DatabaseUsers.ListByUserID(ctx, user.ID, repository.ListOptions{Limit: 10000})
		if err != nil {
			logger.Warn("metadata: list db users", "err", err)
		} else {
			dbs, _, _ := deps.Databases.ListByUserID(ctx, user.ID, repository.ListOptions{Limit: 10000})
			dbName := make(map[string]string, len(dbs))
			for _, d := range dbs {
				dbName[d.ID] = d.Name
			}
			for _, du := range users {
				row := internalbackup.MetadataDatabaseUser{
					ID:           du.ID,
					Username:     du.Username,
					PasswordHash: du.PasswordHash,
					CreatedAt:    du.CreatedAt.Format("2006-01-02T15:04:05Z"),
				}
				if deps.DatabaseGrants != nil {
					grants, err := deps.DatabaseGrants.ListByDatabaseUserID(ctx, du.ID)
					if err != nil {
						logger.Warn("metadata: list grants", "err", err, "database_user_id", du.ID)
					} else {
						for _, g := range grants {
							row.Grants = append(row.Grants, internalbackup.MetadataDatabaseUserGrant{
								DatabaseID:   g.DatabaseID,
								DatabaseName: dbName[g.DatabaseID],
								GrantLevel:   g.GrantLevel,
								Privileges:   g.Privileges,
							})
						}
					}
				}
				m.DatabaseUsers = append(m.DatabaseUsers, row)
			}
		}
	}
	if deps.AppInstalls != nil {
		installs, _, err := deps.AppInstalls.ListByUserID(ctx, user.ID, repository.ListOptions{Limit: 10000})
		if err != nil {
			logger.Warn("metadata: list app installs", "err", err)
		} else {
			for _, ai := range installs {
				dbID := ""
				if ai.DBID != nil {
					dbID = *ai.DBID
				}
				m.AppInstalls = append(m.AppInstalls, internalbackup.MetadataAppInstall{
					ID:            ai.ID,
					UserID:        ai.UserID,
					DomainID:      ai.DomainID,
					DBID:          dbID,
					Version:       strDerefOr(ai.Version, ""),
					AdminUsername: ai.AdminUsername,
					AdminEmail:    ai.AdminEmail,
					Locale:        ai.Locale,
					Status:        ai.Status,
					UseWWW:        ai.UseWWW,
					Subdirectory:  ai.Subdirectory,
					AppType:       ai.AppType,
					CreatedAt:     ai.CreatedAt.Format("2006-01-02T15:04:05Z"),
				})
			}
		}
	}
	return m
}

// userMailboxes returns the email addresses of every mailbox under
// every domain owned by the user. Two-step: domain.list_by_user →
// mailbox.list_by_domain. Errors per-domain are tolerated.
func userMailboxes(ctx context.Context, deps Deps, userID string, logger *slog.Logger) []string {
	if deps.Domains == nil || deps.Mailboxes == nil {
		return nil
	}
	doms, _, err := deps.Domains.ListByUserID(ctx, userID, repository.ListOptions{Limit: 10000})
	if err != nil {
		logger.Warn("list domains for backup failed", "err", err)
		return nil
	}
	var out []string
	for _, d := range doms {
		mbs, _, err := deps.Mailboxes.ListByDomainID(ctx, d.ID, repository.ListOptions{Limit: 10000})
		if err != nil {
			logger.Warn("list mailboxes for backup failed",
				"domain_id", d.ID, "err", err)
			continue
		}
		for _, m := range mbs {
			out = append(out, m.EmailCached)
		}
	}
	return out
}

func strDerefOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
