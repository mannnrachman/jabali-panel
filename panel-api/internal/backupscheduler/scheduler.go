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

// EnqueueInterval is the cadence at which due schedules are scanned and
// enqueued as backup_jobs. Bigger than 60s loses scheduling precision;
// smaller burns DB queries with no operator value.
const EnqueueInterval = 60 * time.Second

// DispatchInterval is the cadence at which queued backup_jobs are
// drained under the concurrency limit. Smaller = quicker reaction when
// a slot frees, but every tick is one settings-fetch + count query.
const DispatchInterval = 10 * time.Second

// MaxDuePerTick caps how many overdue schedules one tick will enqueue.
// Bigger = potential DB pressure on the join queries; smaller = recovery
// from a long pause takes more ticks. 50 fits a 5-minute outage on a
// 1k-user host.
const MaxDuePerTick = 50

// DefaultMaxConcurrent is the fallback when server_settings is missing
// or zero. 2 keeps a fast disk busy without melting restic on a wide
// fan-out.
const DefaultMaxConcurrent = 2

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
	Settings       repository.ServerSettingsRepository
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

// Start runs the enqueue + dispatch loops until ctx is done. Two
// tickers because a 60s enqueue cadence is too slow to react when a
// dispatch slot frees up — drains run every 10s.
func (s *Scheduler) Start(ctx context.Context) {
	enqueueT := time.NewTicker(EnqueueInterval)
	dispatchT := time.NewTicker(DispatchInterval)
	defer enqueueT.Stop()
	defer dispatchT.Stop()
	s.deps.Log.Info("backup scheduler started",
		"enqueue_interval", EnqueueInterval,
		"dispatch_interval", DispatchInterval)
	// Run one of each immediately so a panel restart doesn't leave
	// overdue schedules waiting up to 60s for the next firing.
	s.tickEnqueue(ctx)
	s.tickDispatch(ctx)
	for {
		select {
		case <-ctx.Done():
			s.deps.Log.Info("backup scheduler stopped")
			return
		case <-enqueueT.C:
			s.tickEnqueue(ctx)
		case <-dispatchT.C:
			s.tickDispatch(ctx)
		}
	}
}

func (s *Scheduler) tickEnqueue(ctx context.Context) {
	now := time.Now().UTC()
	due, err := s.deps.Schedules.ListDue(ctx, now, MaxDuePerTick)
	if err != nil {
		s.deps.Log.Error("scheduler list-due failed", "err", err)
		return
	}
	for _, sched := range due {
		s.enqueue(ctx, sched)
	}
}

// tickDispatch reads the current concurrency limit, counts running
// jobs, and dispatches queued rows to fill the gap.
func (s *Scheduler) tickDispatch(ctx context.Context) {
	max := uint32(DefaultMaxConcurrent)
	if s.deps.Settings != nil {
		set, err := s.deps.Settings.Get(ctx)
		if err == nil && set != nil && set.BackupMaxConcurrentJobs > 0 {
			max = set.BackupMaxConcurrentJobs
		}
	}
	running, err := s.deps.Jobs.CountByStatus(ctx, models.BackupJobStatusRunning)
	if err != nil {
		s.deps.Log.Error("dispatcher count-running failed", "err", err)
		return
	}
	slots := int(max) - int(running)
	if slots <= 0 {
		return
	}
	queued, err := s.deps.Jobs.ListQueuedOldest(ctx, slots)
	if err != nil {
		s.deps.Log.Error("dispatcher list-queued failed", "err", err)
		return
	}
	for _, j := range queued {
		s.dispatchOne(ctx, j)
	}
}

// enqueue handles one overdue schedule end-to-end: compute next
// firing, create queued backup_jobs rows for each fan-out target, mark
// the schedule ran. The dispatcher tick then drains the queue.
func (s *Scheduler) enqueue(ctx context.Context, sched models.BackupSchedule) {
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

	enqueued, qErr := s.enqueueBackup(ctx, sched)
	if qErr != nil {
		logger.Error("scheduler enqueue failed", "err", qErr)
		// Still advance next_run_at so we don't tight-loop on a
		// permanent failure (e.g. user deleted but FK didn't cascade).
		_ = s.deps.Schedules.UpdateNextRun(ctx, sched.ID, next)
		return
	}
	if !enqueued {
		// No fan-out targets — advance next_run_at without marking ran.
		_ = s.deps.Schedules.UpdateNextRun(ctx, sched.ID, next)
		return
	}

	if err := s.deps.Schedules.MarkRan(ctx, sched.ID, time.Now().UTC(), next); err != nil {
		logger.Error("schedule mark-ran failed", "err", err)
	}
}

// enqueueBackup creates queued backup_jobs rows for the given
// schedule. The dispatcher tick later picks them up and calls the
// agent under the concurrency cap.
func (s *Scheduler) enqueueBackup(ctx context.Context, sched models.BackupSchedule) (bool, error) {
	switch sched.Kind {
	case models.BackupScheduleKindAccount:
		// Multi-user fan-out via the backup_schedule_users join:
		//   - empty list      → every non-admin user on the host
		//   - non-empty list  → only those specific users
		// The legacy single backup_schedules.user_id column is
		// ignored once the join table is the source of truth.
		anyUser := false
		explicitIDs, err := s.deps.Schedules.GetUserIDs(ctx, sched.ID)
		if err != nil {
			return false, fmt.Errorf("load schedule users: %w", err)
		}
		var targets []models.User
		if len(explicitIDs) == 0 {
			notAdmin := false
			users, _, err := s.deps.Users.List(ctx, repository.ListOptions{
				Limit:   10000,
				IsAdmin: &notAdmin,
			})
			if err != nil {
				return false, fmt.Errorf("list users: %w", err)
			}
			targets = users
		} else {
			for _, uid := range explicitIDs {
				u, err := s.deps.Users.FindByID(ctx, uid)
				if err != nil || u == nil {
					s.deps.Log.Warn("schedule references missing user; skipping",
						"schedule_id", sched.ID, "user_id", uid)
					continue
				}
				if u.IsAdmin {
					s.deps.Log.Warn("schedule references admin; skipping",
						"schedule_id", sched.ID, "user_id", uid)
					continue
				}
				targets = append(targets, *u)
			}
		}
		for i := range targets {
			u := &targets[i]
			if ok := s.enqueueAccountBackup(ctx, sched, u); ok {
				anyUser = true
			}
		}
		// Opt-in: same schedule also fires a system_backup. Errors here
		// are logged + swallowed so a system-side failure can't undo the
		// per-user backups that already succeeded.
		anySystem := false
		if sched.IncludeSystemBackup {
			anySystem = s.enqueueSystemBackup(ctx, sched)
		}
		return anyUser || anySystem, nil

	case models.BackupScheduleKindSystem:
		return s.enqueueSystemBackup(ctx, sched), nil

	default:
		return false, fmt.Errorf("unknown schedule kind %q", sched.Kind)
	}
}

// enqueueAccountBackup creates one backup_jobs row in status=queued
// for the given user. The dispatcher tick later picks it up and calls
// the agent. Returns true on successful insert, false on DB error.
func (s *Scheduler) enqueueAccountBackup(ctx context.Context, sched models.BackupSchedule, user *models.User) bool {
	logger := s.deps.Log.With("schedule_id", sched.ID, "user_id", user.ID)
	schedID := sched.ID
	job := &models.BackupJob{
		ID:         ids.NewULID(),
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
	return true
}

// enqueueSystemBackup creates one backup_jobs row in status=queued
// for a system backup. Used by both the dedicated system_backup
// schedule kind and the include_system_backup opt-in on account
// schedules.
func (s *Scheduler) enqueueSystemBackup(ctx context.Context, sched models.BackupSchedule) bool {
	logger := s.deps.Log.With("schedule_id", sched.ID, "kind", "system_backup")
	schedID := sched.ID
	job := &models.BackupJob{
		ID:         ids.NewULID(),
		UserID:     "system",
		ScheduleID: &schedID,
		Kind:       models.BackupJobKindSystemBackup,
		Status:     models.BackupJobStatusQueued,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.deps.Jobs.Create(ctx, job); err != nil {
		logger.Error("create system backup_job failed", "err", err)
		return false
	}
	return true
}

// dispatchOne sends one queued job to the agent and flips it to
// running. Errors are logged + the row is marked failed so the
// finalizer doesn't leave it stuck in queued forever.
func (s *Scheduler) dispatchOne(ctx context.Context, j models.BackupJob) {
	switch j.Kind {
	case models.BackupJobKindAccountBackup:
		s.dispatchAccount(ctx, j)
	case models.BackupJobKindSystemBackup:
		s.dispatchSystem(ctx, j)
	default:
		s.deps.Log.Warn("dispatcher: unknown job kind, skipping",
			"job_id", j.ID, "kind", j.Kind)
	}
}

func (s *Scheduler) dispatchAccount(ctx context.Context, j models.BackupJob) {
	logger := s.deps.Log.With("job_id", j.ID, "user_id", j.UserID)
	user, err := s.deps.Users.FindByID(ctx, j.UserID)
	if err != nil || user == nil {
		_ = s.deps.Jobs.MarkFinished(ctx, j.ID, models.BackupJobStatusFailed,
			"", "", 0, 0, nil, nil, "user not found at dispatch time")
		logger.Warn("dispatcher: user lookup failed; marking failed", "err", err)
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dbs := userDatabases(callCtx, s.deps, user.ID, logger)
	mbs := userMailboxes(callCtx, s.deps, user.ID, logger)
	meta := buildScheduleMetadata(callCtx, s.deps, user, logger)
	scheduleID := ""
	if j.ScheduleID != nil {
		scheduleID = *j.ScheduleID
	}
	params := map[string]any{
		"job_id":      j.ID,
		"user_id":     user.ID,
		"username":    user.Username,
		"email":       user.Email,
		"is_admin":    user.IsAdmin,
		"databases":   dbs,
		"mailboxes":   mbs,
		"metadata":    meta,
		"schedule_id": scheduleID,
	}
	if _, err := s.deps.Agent.Call(callCtx, "backup.create", params); err != nil {
		if isAgentConflict(err) {
			_ = s.deps.Jobs.MarkFinished(ctx, j.ID, models.BackupJobStatusCancelled,
				"", "", 0, 0, nil, nil, "skipped: prior backup still running")
			return
		}
		_ = s.deps.Jobs.MarkFinished(ctx, j.ID, models.BackupJobStatusFailed,
			"", "", 0, 0, nil, nil, err.Error())
		logger.Error("agent backup.create failed", "err", err)
		return
	}
	_ = s.deps.Jobs.MarkStarted(ctx, j.ID)
}

func (s *Scheduler) dispatchSystem(ctx context.Context, j models.BackupJob) {
	logger := s.deps.Log.With("job_id", j.ID, "kind", "system_backup")
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	scheduleID := ""
	if j.ScheduleID != nil {
		scheduleID = *j.ScheduleID
	}
	params := map[string]any{
		"job_id":           j.ID,
		"include_accounts": false,
		"schedule_id":      scheduleID,
	}
	if _, err := s.deps.Agent.Call(callCtx, "system.backup", params); err != nil {
		if isAgentConflict(err) {
			_ = s.deps.Jobs.MarkFinished(ctx, j.ID, models.BackupJobStatusCancelled,
				"", "", 0, 0, nil, nil, "skipped: prior system backup still running")
			return
		}
		_ = s.deps.Jobs.MarkFinished(ctx, j.ID, models.BackupJobStatusFailed,
			"", "", 0, 0, nil, nil, err.Error())
		logger.Error("agent system.backup failed", "err", err)
		return
	}
	_ = s.deps.Jobs.MarkStarted(ctx, j.ID)
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
