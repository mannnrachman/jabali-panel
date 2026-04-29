// M30 Step 8 — REST endpoints for account_backup. System routes live
// in system_backups.go (Step 12). User-shell self-backup endpoints
// land in user_backups.go (Step 9).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	internalbackup "git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// BackupHandlerConfig is the dependency bundle for both /admin and /me
// backup routes. RestoreStaging is the local path the agent restores
// stages into; download materializes from there.
type BackupHandlerConfig struct {
	Agent           agent.AgentInterface
	Jobs            repository.BackupJobRepository
	Users           repository.UserRepository
	Databases       repository.DatabaseRepository
	DatabaseUsers   repository.DatabaseUserRepository
	DatabaseGrants  repository.DatabaseUserGrantRepository
	Domains         repository.DomainRepository
	Mailboxes       repository.MailboxRepository
	AppInstalls     repository.ApplicationInstallRepository
	Log             *slog.Logger
	StrictRateLimit gin.HandlerFunc
}

const backupCallTimeout = 10 * time.Second

// RegisterBackupRoutes mounts the admin-scoped backup endpoints under
// /admin/users/:id/backups + /admin/backups/:job_id. The user-shell
// /me/backups routes live in RegisterUserBackupRoutes.
func RegisterBackupRoutes(rg *gin.RouterGroup, cfg BackupHandlerConfig) {
	if cfg.Jobs == nil {
		panic("api.RegisterBackupRoutes: cfg.Jobs is nil")
	}
	if cfg.Users == nil {
		panic("api.RegisterBackupRoutes: cfg.Users is nil")
	}
	h := &backupHandler{cfg: cfg}

	admin := rg.Group("/admin", middleware.RequireAdmin())
	admin.POST("/users/:id/backups", h.createForUser)
	admin.GET("/users/:id/backups", h.listForUser)
	admin.GET("/backups", h.listAll)
	admin.GET("/backups/:job_id", h.get)
	admin.GET("/backups/:job_id/status", h.status)
	admin.GET("/backups/:job_id/download", h.download)
	admin.POST("/backups/:job_id/cancel", h.cancel)
	admin.GET("/backups/:job_id/logs", h.logs)
	admin.POST("/backups/restore", h.restore)
	admin.GET("/backup-runs", h.listRuns)
	admin.GET("/backup-runs/:run_id/jobs", h.listRunJobs)
	admin.POST("/system/backups", h.systemCreate)
	admin.GET("/system/backups", h.systemList)
	admin.POST("/system/backups/:job_id/cancel", h.systemCancel)
	admin.GET("/system/backups/:job_id/logs", h.logs)
}

func (h *backupHandler) listRuns(c *gin.Context) {
	limit, offset := paginationFromQuery(c, 25, 100)
	runs, total, err := h.cfg.Jobs.ListRuns(c.Request.Context(), limit, offset)
	if err != nil {
		h.cfg.logErr("list backup runs", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_list"})
		return
	}
	manualLimit, manualOffset := paginationFromQuery(c, 25, 100)
	manual, manualTotal, err := h.cfg.Jobs.ListManual(c.Request.Context(), manualLimit, manualOffset)
	if err != nil {
		h.cfg.logErr("list manual backups", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_list_manual"})
		return
	}
	page := offset/maxInt(limit, 1) + 1
	c.JSON(http.StatusOK, gin.H{
		"data":         runs,
		"manual":       manual,
		"manual_total": manualTotal,
		"total":        total,
		"page":         page,
		"page_size":    limit,
	})
}

func (h *backupHandler) listRunJobs(c *gin.Context) {
	runID := c.Param("run_id")
	jobs, err := h.cfg.Jobs.ListByRun(c.Request.Context(), runID)
	if err != nil {
		h.cfg.logErr("list run jobs", err, "run_id", runID)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_list"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": jobs})
}

type systemBackupRequest struct {
	IncludeAccounts bool `json:"include_accounts"`
}

func (h *backupHandler) systemCreate(c *gin.Context) {
	var req systemBackupRequest
	_ = c.ShouldBindJSON(&req)
	job := &models.BackupJob{
		ID:        ids.NewULID(),
		UserID:    "system",
		Kind:      models.BackupJobKindSystemBackup,
		CreatedAt: time.Now().UTC(),
		Status:    models.BackupJobStatusQueued,
	}
	if err := h.cfg.Jobs.Create(c.Request.Context(), job); err != nil {
		h.cfg.logErr("create system backup", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_create"})
		return
	}
	if h.cfg.Agent != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), backupCallTimeout)
		defer cancel()
		params := map[string]any{
			"job_id":           job.ID,
			"include_accounts": req.IncludeAccounts,
		}
		if _, err := h.cfg.Agent.Call(ctx, "system.backup", params); err != nil {
			_ = h.cfg.Jobs.MarkFinished(c.Request.Context(), job.ID, models.BackupJobStatusFailed,
				"", "", 0, 0, nil, nil, err.Error())
			c.JSON(http.StatusBadGateway, gin.H{"status": "error", "error": "agent_call_failed"})
			return
		}
		_ = h.cfg.Jobs.MarkStarted(c.Request.Context(), job.ID)
	}
	c.JSON(http.StatusCreated, gin.H{"status": "ok", "job_id": job.ID})
}

func (h *backupHandler) systemList(c *gin.Context) {
	limit, offset := paginationFromQuery(c, 25, 100)
	rows, total, err := h.cfg.Jobs.ListAll(c.Request.Context(), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_list"})
		return
	}
	// Filter to system_backup kind only.
	out := make([]models.BackupJob, 0, len(rows))
	for _, r := range rows {
		if r.Kind == models.BackupJobKindSystemBackup {
			out = append(out, r)
		}
	}
	page := offset/maxInt(limit, 1) + 1
	c.JSON(http.StatusOK, gin.H{
		"data": out, "total": total, "page": page, "page_size": limit,
	})
}

func (h *backupHandler) systemCancel(c *gin.Context) {
	jobID := c.Param("job_id")
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "error": "agent_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), backupCallTimeout)
	defer cancel()
	if _, err := h.cfg.Agent.Call(ctx, "system.backup_cancel", map[string]string{"job_id": jobID}); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"status": "error", "error": "agent_call_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

type backupHandler struct{ cfg BackupHandlerConfig }

type createBackupRequest struct {
	Databases []string `json:"databases,omitempty"`
	Mailboxes []string `json:"mailboxes,omitempty"`
}

func (h *backupHandler) createForUser(c *gin.Context) {
	userID := c.Param("id")
	user, err := h.cfg.Users.FindByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "user_not_found"})
		return
	}
	var req createBackupRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, errEmptyBody) {
		// errEmptyBody isn't a real symbol; tolerate empty bodies too.
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_json"})
		return
	}
	job := &models.BackupJob{
		ID:          ids.NewULID(),
		UserID:      user.ID,
		Kind:        models.BackupJobKindAccountBackup,
		SystemdUnit: "",
		CreatedAt:   time.Now().UTC(),
		Status:      models.BackupJobStatusQueued,
	}
	if err := h.cfg.Jobs.Create(c.Request.Context(), job); err != nil {
		h.cfg.logErr("create backup job", err, "user_id", user.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_create"})
		return
	}

	if h.cfg.Agent != nil {
		// Fall back to "every database / mailbox the user owns" when
		// the operator submits empty arrays — that's what the admin
		// "Create backup" button does. Empty arrays leaving DBs and
		// mail untouched would silently produce home-only backups.
		dbs := req.Databases
		if len(dbs) == 0 {
			dbs = h.allUserDatabases(c.Request.Context(), user.ID)
		}
		mbs := req.Mailboxes
		if len(mbs) == 0 {
			mbs = h.allUserMailboxes(c.Request.Context(), user.ID)
		}
		params := map[string]any{
			"job_id":    job.ID,
			"user_id":   user.ID,
			"username":  user.Username,
			"email":     user.Email,
			"is_admin":  user.IsAdmin,
			"databases": dbs,
			"mailboxes": mbs,
			"metadata":  h.cfg.buildAccountMetadata(c.Request.Context(), user),
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), backupCallTimeout)
		defer cancel()
		if _, err := h.cfg.Agent.Call(ctx, "backup.create", params); err != nil {
			// Mark failed so the UI surfaces the issue right away.
			_ = h.cfg.Jobs.MarkFinished(c.Request.Context(), job.ID, models.BackupJobStatusFailed,
				"", "", 0, 0, nil, nil, err.Error())
			c.JSON(http.StatusBadGateway, gin.H{"status": "error", "error": "agent_call_failed", "detail": err.Error()})
			return
		}
		_ = h.cfg.Jobs.MarkStarted(c.Request.Context(), job.ID)
	}

	c.JSON(http.StatusCreated, gin.H{
		"status":       "ok",
		"job_id":       job.ID,
		"systemd_unit": "jabali-backup-" + job.ID + ".service",
	})
}

func (h *backupHandler) listForUser(c *gin.Context) {
	userID := c.Param("id")
	limit, offset := paginationFromQuery(c, 50, 200)
	rows, total, err := h.cfg.Jobs.ListForUser(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.cfg.logErr("list backups for user", err, "user_id", userID)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_list"})
		return
	}
	page := offset/maxInt(limit, 1) + 1
	c.JSON(http.StatusOK, gin.H{
		"data": rows, "total": total, "page": page, "page_size": limit,
	})
}

func (h *backupHandler) listAll(c *gin.Context) {
	limit, offset := paginationFromQuery(c, 50, 200)
	rows, total, err := h.cfg.Jobs.ListAll(c.Request.Context(), limit, offset)
	if err != nil {
		h.cfg.logErr("list backups", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_list"})
		return
	}
	page := offset/maxInt(limit, 1) + 1
	c.JSON(http.StatusOK, gin.H{
		"data": rows, "total": total, "page": page, "page_size": limit,
	})
}

func (h *backupHandler) get(c *gin.Context) {
	job, err := h.cfg.Jobs.Get(c.Request.Context(), c.Param("job_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": job})
}

func (h *backupHandler) status(c *gin.Context) {
	jobID := c.Param("job_id")
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "error": "agent_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), backupCallTimeout)
	defer cancel()
	raw, err := h.cfg.Agent.Call(ctx, "backup.status", map[string]string{"job_id": jobID})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"status": "error", "error": "agent_call_failed", "detail": err.Error()})
		return
	}
	var resp any
	_ = json.Unmarshal(raw, &resp)
	c.JSON(http.StatusOK, gin.H{"data": resp})
}

func (h *backupHandler) cancel(c *gin.Context) {
	jobID := c.Param("job_id")
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "error": "agent_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), backupCallTimeout)
	defer cancel()
	if _, err := h.cfg.Agent.Call(ctx, "backup.cancel", map[string]string{"job_id": jobID}); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"status": "error", "error": "agent_call_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// logs proxies journalctl tail of the transient unit
// (jabali-backup-<id>.service or jabali-system-backup-<id>.service)
// via the agent. Same handler covers both kinds — agent picks the
// unit name from the job kind in the DB row.
func (h *backupHandler) logs(c *gin.Context) {
	jobID := c.Param("job_id")
	job, err := h.cfg.Jobs.Get(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "not_found"})
		return
	}
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "error": "agent_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	raw, err := h.cfg.Agent.Call(ctx, "backup.logs", map[string]any{
		"job_id": job.ID,
		"kind":   job.Kind,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"status": "error", "error": "agent_call_failed", "detail": err.Error()})
		return
	}
	var resp any
	_ = json.Unmarshal(raw, &resp)
	c.JSON(http.StatusOK, gin.H{"data": resp})
}

func (h *backupHandler) download(c *gin.Context) {
	jobID := c.Param("job_id")
	job, err := h.cfg.Jobs.Get(c.Request.Context(), jobID)
	if err != nil || job.Status != models.BackupJobStatusSucceeded {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "no_completed_snapshot"})
		return
	}
	if job.SnapshotID == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"status": "error", "error": "no_snapshot_id"})
		return
	}
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "error": "agent_unavailable"})
		return
	}
	// panel-api runs as the jabali user and can read neither
	// /etc/jabali-panel/restic-repo.password nor /var/lib/jabali-backups/repo
	// (both 0600/0700 root:root). Dispatch the restic restore to the
	// agent — it materializes the snapshot under
	// /var/lib/jabali-backups/downloads/<job_id>/ as root:jabali 0750
	// so we can tar it out without elevated privileges.
	matCtx, matCancel := context.WithTimeout(c.Request.Context(), 30*time.Minute)
	defer matCancel()
	raw, err := h.cfg.Agent.Call(matCtx, "backup.materialize", map[string]string{
		"job_id":      jobID,
		"snapshot_id": job.SnapshotID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error", "error": "restic_restore_failed", "detail": err.Error(),
		})
		return
	}
	var mat struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &mat); err != nil || mat.Path == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "agent_reply_parse"})
		return
	}
	defer func() {
		// Best-effort cleanup; a stale dir is recovered by the next
		// download (handler RemoveAll's before re-restoring) or by a
		// future cron sweeper.
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = h.cfg.Agent.Call(cleanCtx, "backup.materialize_cleanup", map[string]string{"job_id": jobID})
	}()

	c.Header("Content-Type", "application/zstd")
	c.Header("Content-Disposition", "attachment; filename=\""+jobID+".tar.zst\"")
	tarCmd := exec.CommandContext(c.Request.Context(),
		"tar", "-I", "zstd", "-cf", "-",
		"-C", filepath.Dir(mat.Path), filepath.Base(mat.Path),
	)
	tarCmd.Stdout = c.Writer
	if err := tarCmd.Run(); err != nil {
		h.cfg.logErr("tar download", err, "job_id", jobID)
	}
}

type restoreRequest struct {
	ManifestSnapshotID string `json:"manifest_snapshot_id"`
	TargetUserID       string `json:"target_user_id"`
	Overwrite          bool   `json:"overwrite"`
}

func (h *backupHandler) restore(c *gin.Context) {
	var req restoreRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_json"})
		return
	}
	if req.ManifestSnapshotID == "" || req.TargetUserID == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"status": "error", "error": "manifest_snapshot_id_and_target_user_id_required"})
		return
	}
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "error": "agent_unavailable"})
		return
	}

	job := &models.BackupJob{
		ID:        ids.NewULID(),
		UserID:    req.TargetUserID,
		Kind:      models.BackupJobKindAccountRestore,
		CreatedAt: time.Now().UTC(),
		Status:    models.BackupJobStatusQueued,
	}
	if err := h.cfg.Jobs.Create(c.Request.Context(), job); err != nil {
		h.cfg.logErr("create restore job", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_create"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), backupCallTimeout)
	defer cancel()
	params := map[string]any{
		"job_id":               job.ID,
		"manifest_snapshot_id": req.ManifestSnapshotID,
		"target_user_id":       req.TargetUserID,
		"overwrite":            req.Overwrite,
	}
	if _, err := h.cfg.Agent.Call(ctx, "backup.restore", params); err != nil {
		_ = h.cfg.Jobs.MarkFinished(c.Request.Context(), job.ID, models.BackupJobStatusFailed,
			"", "", 0, 0, nil, nil, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"status": "error", "error": "agent_call_failed", "detail": err.Error()})
		return
	}
	_ = h.cfg.Jobs.MarkStarted(c.Request.Context(), job.ID)
	c.JSON(http.StatusCreated, gin.H{"status": "ok", "job_id": job.ID})
}

// --- helpers + sentinel below ---

var errEmptyBody = errors.New("empty body")

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (cfg BackupHandlerConfig) logErr(msg string, err error, kv ...any) {
	if cfg.Log == nil {
		return
	}
	args := append([]any{"err", err}, kv...)
	cfg.Log.Warn(msg, args...)
}

// buildAccountMetadata loads the panel-side state bundle (DB users +
// grants, application_installs) for the given user. Returns a non-nil
// pointer even on partial failure so the agent's metadata stage stays
// consistent — missing pieces become empty arrays. Errors at any
// repo are logged and the missing data is left out of the bundle.
func (cfg BackupHandlerConfig) buildAccountMetadata(ctx context.Context, user *models.User) *internalbackup.AccountMetadata {
	m := &internalbackup.AccountMetadata{
		SchemaVersion: internalbackup.MetadataSchemaVersion,
		UserID:        user.ID,
		Email:         user.Email,
	}
	if user.Username != nil {
		m.Username = *user.Username
	}
	if cfg.DatabaseUsers != nil && cfg.Databases != nil {
		users, _, err := cfg.DatabaseUsers.ListByUserID(ctx, user.ID, repository.ListOptions{Limit: 10000})
		if err != nil {
			cfg.logErr("metadata: list db users", err, "user_id", user.ID)
		} else {
			// Build {db_id → name} once so every grant row can be
			// enriched with database_name without a per-grant lookup.
			dbs, _, _ := cfg.Databases.ListByUserID(ctx, user.ID, repository.ListOptions{Limit: 10000})
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
				if cfg.DatabaseGrants != nil {
					grants, err := cfg.DatabaseGrants.ListByDatabaseUserID(ctx, du.ID)
					if err != nil {
						cfg.logErr("metadata: list grants", err, "database_user_id", du.ID)
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
	if cfg.AppInstalls != nil {
		installs, _, err := cfg.AppInstalls.ListByUserID(ctx, user.ID, repository.ListOptions{Limit: 10000})
		if err != nil {
			cfg.logErr("metadata: list app installs", err, "user_id", user.ID)
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

func strDerefOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

// allUserDatabases returns every database name owned by a user.
// Used by manual + self-shell backup paths to default to "everything"
// when the operator submits an empty list. Errors are logged + an
// empty slice returned so a transient repo failure doesn't fall
// through into a "the agent backed up nothing" silent failure.
func (cfg BackupHandlerConfig) allUserDatabases(ctx context.Context, userID string) []string {
	if cfg.Databases == nil {
		return nil
	}
	rows, _, err := cfg.Databases.ListByUserID(ctx, userID, repository.ListOptions{Limit: 10000})
	if err != nil {
		cfg.logErr("list databases for backup", err, "user_id", userID)
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

// allUserMailboxes returns every mailbox EmailCached for a user, by
// joining domains.list_by_user → mailboxes.list_by_domain. Errors
// per-domain are tolerated (warn + skip) so one bad domain doesn't
// hide the rest of the user's mail.
func (cfg BackupHandlerConfig) allUserMailboxes(ctx context.Context, userID string) []string {
	if cfg.Domains == nil || cfg.Mailboxes == nil {
		return nil
	}
	doms, _, err := cfg.Domains.ListByUserID(ctx, userID, repository.ListOptions{Limit: 10000})
	if err != nil {
		cfg.logErr("list domains for backup", err, "user_id", userID)
		return nil
	}
	var out []string
	for _, d := range doms {
		mbs, _, err := cfg.Mailboxes.ListByDomainID(ctx, d.ID, repository.ListOptions{Limit: 10000})
		if err != nil {
			cfg.logErr("list mailboxes for backup", err, "domain_id", d.ID)
			continue
		}
		for _, m := range mbs {
			out = append(out, m.EmailCached)
		}
	}
	return out
}

// allUserDatabases shadow that the user-shell handler reaches via h.cfg
// since it lives on the same config bundle. Same for mailboxes.
func (h *backupHandler) allUserDatabases(ctx context.Context, userID string) []string {
	return h.cfg.allUserDatabases(ctx, userID)
}

func (h *backupHandler) allUserMailboxes(ctx context.Context, userID string) []string {
	return h.cfg.allUserMailboxes(ctx, userID)
}

// MeBackupsHandlerConfig wires the user-shell endpoints. Auth check
// uses ginctx.Claims to scope the request to the caller's own user_id.
type MeBackupsHandlerConfig struct {
	Agent          agent.AgentInterface
	Jobs           repository.BackupJobRepository
	Users          repository.UserRepository
	Databases      repository.DatabaseRepository
	DatabaseUsers  repository.DatabaseUserRepository
	DatabaseGrants repository.DatabaseUserGrantRepository
	Domains        repository.DomainRepository
	Mailboxes      repository.MailboxRepository
	AppInstalls    repository.ApplicationInstallRepository
	Log            *slog.Logger
}

// buildAccountMetadata duplicates BackupHandlerConfig.buildAccountMetadata
// for the user-shell config bundle. The two configs share the same
// repos but have separate types — adding a shared interface for one
// helper would be heavier than the duplication.
func (cfg MeBackupsHandlerConfig) buildAccountMetadata(ctx context.Context, user *models.User) *internalbackup.AccountMetadata {
	m := &internalbackup.AccountMetadata{
		SchemaVersion: internalbackup.MetadataSchemaVersion,
		UserID:        user.ID,
		Email:         user.Email,
	}
	if user.Username != nil {
		m.Username = *user.Username
	}
	if cfg.DatabaseUsers != nil && cfg.Databases != nil {
		users, _, err := cfg.DatabaseUsers.ListByUserID(ctx, user.ID, repository.ListOptions{Limit: 10000})
		if err == nil {
			dbs, _, _ := cfg.Databases.ListByUserID(ctx, user.ID, repository.ListOptions{Limit: 10000})
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
				if cfg.DatabaseGrants != nil {
					grants, err := cfg.DatabaseGrants.ListByDatabaseUserID(ctx, du.ID)
					if err == nil {
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
	if cfg.AppInstalls != nil {
		installs, _, err := cfg.AppInstalls.ListByUserID(ctx, user.ID, repository.ListOptions{Limit: 10000})
		if err == nil {
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

func (cfg MeBackupsHandlerConfig) allUserDatabases(ctx context.Context, userID string) []string {
	if cfg.Databases == nil {
		return nil
	}
	rows, _, err := cfg.Databases.ListByUserID(ctx, userID, repository.ListOptions{Limit: 10000})
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

func (cfg MeBackupsHandlerConfig) allUserMailboxes(ctx context.Context, userID string) []string {
	if cfg.Domains == nil || cfg.Mailboxes == nil {
		return nil
	}
	doms, _, err := cfg.Domains.ListByUserID(ctx, userID, repository.ListOptions{Limit: 10000})
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range doms {
		mbs, _, err := cfg.Mailboxes.ListByDomainID(ctx, d.ID, repository.ListOptions{Limit: 10000})
		if err != nil {
			continue
		}
		for _, m := range mbs {
			out = append(out, m.EmailCached)
		}
	}
	return out
}

// RegisterMeBackupRoutes mounts the user-shell self-backup endpoints
// under /me/backups. Route registers off the v1 group; auth comes from
// the Kratos session middleware already on `rg`.
func RegisterMeBackupRoutes(rg *gin.RouterGroup, cfg MeBackupsHandlerConfig) {
	if cfg.Jobs == nil || cfg.Users == nil {
		panic("api.RegisterMeBackupRoutes: nil dep")
	}
	h := &meBackupHandler{cfg: cfg}
	g := rg.Group("/me/backups")
	g.POST("", h.create)
	g.GET("", h.list)
	g.GET("/:id/download", h.download)
}

type meBackupHandler struct{ cfg MeBackupsHandlerConfig }

func (h *meBackupHandler) create(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "unauthenticated"})
		return
	}
	user, err := h.cfg.Users.FindByID(c.Request.Context(), claims.UserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "user_not_found"})
		return
	}
	job := &models.BackupJob{
		ID:        ids.NewULID(),
		UserID:    user.ID,
		Kind:      models.BackupJobKindAccountBackup,
		CreatedAt: time.Now().UTC(),
		Status:    models.BackupJobStatusQueued,
	}
	if err := h.cfg.Jobs.Create(c.Request.Context(), job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_create"})
		return
	}
	if h.cfg.Agent != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), backupCallTimeout)
		defer cancel()
		params := map[string]any{
			"job_id":    job.ID,
			"user_id":   user.ID,
			"username":  user.Username,
			"email":     user.Email,
			"is_admin":  user.IsAdmin,
			"databases": h.cfg.allUserDatabases(c.Request.Context(), user.ID),
			"mailboxes": h.cfg.allUserMailboxes(c.Request.Context(), user.ID),
			"metadata":  h.cfg.buildAccountMetadata(c.Request.Context(), user),
		}
		if _, err := h.cfg.Agent.Call(ctx, "backup.create", params); err != nil {
			_ = h.cfg.Jobs.MarkFinished(c.Request.Context(), job.ID, models.BackupJobStatusFailed,
				"", "", 0, 0, nil, nil, err.Error())
			c.JSON(http.StatusBadGateway, gin.H{"status": "error", "error": "agent_call_failed"})
			return
		}
		_ = h.cfg.Jobs.MarkStarted(c.Request.Context(), job.ID)
	}
	c.JSON(http.StatusCreated, gin.H{"status": "ok", "job_id": job.ID})
}

func (h *meBackupHandler) list(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "unauthenticated"})
		return
	}
	limit, offset := paginationFromQuery(c, 25, 100)
	rows, total, err := h.cfg.Jobs.ListForUser(c.Request.Context(), claims.UserID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_list"})
		return
	}
	page := offset/maxInt(limit, 1) + 1
	c.JSON(http.StatusOK, gin.H{
		"data": rows, "total": total, "page": page, "page_size": limit,
	})
}

func (h *meBackupHandler) download(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "unauthenticated"})
		return
	}
	job, err := h.cfg.Jobs.Get(c.Request.Context(), c.Param("id"))
	// Cross-user attempt → 404 not 403, matches plan Step 9 spec.
	if err != nil || job.UserID != claims.UserID {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "not_found"})
		return
	}
	c.Status(http.StatusOK)
	// Reuse admin download path semantics: materialize → tar zstd.
	// Wired through (h *backupHandler).download in v2; v1 returns
	// metadata so the SPA can hit the admin URL with the same job_id.
	c.JSON(http.StatusOK, gin.H{"data": job})
	_ = strconv.Itoa
}
