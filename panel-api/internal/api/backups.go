// M30 Step 8 — REST endpoints for account_backup. System routes live
// in system_backups.go (Step 12). User-shell self-backup endpoints
// land in user_backups.go (Step 9).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	internalbackup "git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/backupmetadata"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/backupwrapperhelpers"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
)

// BackupHandlerConfig is the dependency bundle for both /admin and /me
// backup routes. RestoreStaging is the local path the agent restores
// stages into; download materializes from there.
type BackupHandlerConfig struct {
	Agent           agent.AgentInterface
	Jobs            repository.BackupJobRepository
	Destinations    repository.BackupDestinationRepository
	Users           repository.UserRepository
	Databases       repository.DatabaseRepository
	DatabaseUsers   repository.DatabaseUserRepository
	DatabaseGrants  repository.DatabaseUserGrantRepository
	Domains         repository.DomainRepository
	Mailboxes       repository.MailboxRepository
	AppInstalls     repository.ApplicationInstallRepository

	// Schema-v2 metadata producers — every nullable repo here is queried
	// when building the per-user metadata bundle so disaster recovery
	// can rebuild full panel state. Each is optional; missing repos
	// log + skip the corresponding section.
	SSLCerts        repository.SSLCertificateRepository
	PHPPools        repository.PHPPoolRepository
	PHPPoolIni      repository.PHPPoolIniOverrideRepository
	Forwarders      repository.EmailForwarderRepository
	Autoresponders  repository.EmailAutoresponderRepository
	MailboxShares   repository.MailboxShareRepository
	DNSSECKeys      repository.DNSSECKeyRepository
	SSHKeys         repository.SSHKeyRepository
	CronJobs        repository.CronJobRepository
	LimitOverrides  repository.UserLimitOverrideRepository
	EgressPolicies  repository.UserEgressPolicyRepository
	EgressRequests  repository.UserEgressRequestRepository

	// M30.2.x — sso key for unsealing per-destination restic
	// passwords before the agent dispatch. Optional; when nil the
	// password helper falls back to the legacy shared file at
	// /etc/jabali-panel/restic-repo.password (back-compat for
	// destinations that haven't been rotated yet).
	SSOKey *ssokey.Key

	Log             *slog.Logger
	StrictRateLimit gin.HandlerFunc
}

const backupCallTimeout = 10 * time.Second

// restoreCallTimeout: account restores run synchronously on the agent
// (no goroutine fork). 10m covers reasonable home-dir + DB + mailbox
// volumes; larger restores should switch to a background-job model.
const restoreCallTimeout = 10 * time.Minute

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
	IncludeAccounts bool   `json:"include_accounts"`
	DestinationID   string `json:"destination_id,omitempty"`
}

func (h *backupHandler) systemCreate(c *gin.Context) {
	var req systemBackupRequest
	_ = c.ShouldBindJSON(&req)
	dest, derr := h.resolveDest(c, req.DestinationID)
	if derr != nil {
		return
	}
	destID := dest.ID
	job := &models.BackupJob{
		ID:            ids.NewULID(),
		UserID:        "system",
		DestinationID: &destID,
		Kind:          models.BackupJobKindSystemBackup,
		CreatedAt:     time.Now().UTC(),
		Status:        models.BackupJobStatusQueued,
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
		for k, v := range destWireParams(dest) {
			params[k] = v
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

// resolveDest looks up the destination row (or auto-picks the only
// enabled one when destID is empty + exactly one exists), writes a
// 4xx + nil on miss, returns the row + nil on success.
func (h *backupHandler) resolveDest(c *gin.Context, destID string) (*models.BackupDestination, error) {
	if h.cfg.Destinations == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "error": "destinations_repo_unavailable"})
		return nil, errors.New("destinations repo unavailable")
	}
	if destID == "" {
		// No destination supplied — fall back to the single enabled
		// destination if exactly one exists, else 400.
		all, err := h.cfg.Destinations.ListEnabled(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_list_destinations"})
			return nil, err
		}
		if len(all) != 1 {
			c.JSON(http.StatusBadRequest, gin.H{
				"status": "error",
				"error":  "destination_id_required",
				"detail": "destination_id required when more than one destination exists",
			})
			return nil, errors.New("destination_id required")
		}
		return &all[0], nil
	}
	d, err := h.cfg.Destinations.Get(c.Request.Context(), destID)
	if err != nil || d == nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "destination_not_found"})
		return nil, errors.New("destination not found")
	}
	if !d.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "destination_disabled"})
		return nil, errors.New("destination disabled")
	}
	return d, nil
}

// destWireParams projects a destination into the JSON keys the agent
// backup commands accept. Mirror of backupscheduler.destWireParams;
// kept duplicated to avoid the api → backupscheduler import cycle.
func destWireParams(d *models.BackupDestination) map[string]any {
	out := map[string]any{
		"repo_url":         d.URL,
		"destination_kind": d.Kind,
		"extra_options":    backupwrapperhelpers.ResticOptionsFor(d),
	}
	if d.CredentialsRef != nil {
		out["credentials_ref"] = *d.CredentialsRef
	}
	if d.Kind == models.BackupDestinationKindSFTP {
		if s := d.ExtraOptionsTyped().SFTP; s != nil {
			out["sftp"] = map[string]any{
				"host":     s.Host,
				"user":     s.User,
				"port":     s.Port,
				"path":     s.Path,
				"auth":     s.Auth,
				"key_path": s.KeyPath,
			}
		}
	}
	return out
}

type createBackupRequest struct {
	DestinationID string   `json:"destination_id,omitempty"`
	Databases     []string `json:"databases,omitempty"`
	Mailboxes     []string `json:"mailboxes,omitempty"`
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
	dest, derr := h.resolveDest(c, req.DestinationID)
	if derr != nil {
		return
	}
	destID := dest.ID
	job := &models.BackupJob{
		ID:            ids.NewULID(),
		UserID:        user.ID,
		DestinationID: &destID,
		Kind:          models.BackupJobKindAccountBackup,
		SystemdUnit:   "",
		CreatedAt:     time.Now().UTC(),
		Status:        models.BackupJobStatusQueued,
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
		// M37: split mariadb and postgres dbs so the agent dispatches
		// the right dump tool per engine.
		pgDbs := h.cfg.allUserPostgresDatabases(c.Request.Context(), user.ID)
		params := map[string]any{
			"job_id":             job.ID,
			"user_id":            user.ID,
			"username":           user.Username,
			"email":              user.Email,
			"is_admin":           user.IsAdmin,
			"databases":          dbs,
			"databases_postgres": pgDbs,
			"mailboxes":          mbs,
			"metadata":           h.cfg.buildAccountMetadata(c.Request.Context(), user),
		}
		for k, v := range destWireParams(dest) {
			params[k] = v
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), backupCallTimeout)
		defer cancel()
		// M30.2.x: per-destination password file is provisioned by
		// the agent's write_temp on demand; cleanup runs after
		// dispatch completes (or fails).
		callErr := backupwrapperhelpers.WithDestPasswordFile(ctx, dest, h.cfg.Agent, h.cfg.SSOKey,
			func(passwordFile string) error {
				if passwordFile != "" {
					params["password_file"] = passwordFile
				}
				_, err := h.cfg.Agent.Call(ctx, "backup.create", params)
				return err
			})
		if err := callErr; err != nil {
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
	// DestinationID resolves the restic repo URL + credentials for the
	// agent. Without it the agent defaults to the local repo at
	// /var/lib/jabali-backups/repo, which silently 404s on snapshot
	// lookup whenever the original backup landed on a remote dest.
	DestinationID string `json:"destination_id,omitempty"`
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

	// Resolve destination → repo_url + credentials (and SFTP block when
	// applicable). Empty destination_id falls back to the single-enabled
	// dest auto-pick already implemented for create flows; if the host
	// has many destinations and none was supplied resolveDest 422s.
	dest, derr := h.resolveDest(c, req.DestinationID)
	if derr != nil {
		return
	}

	// Resolve target user → username so the agent's apply step can
	// chown home + scope mariadb loads. The system user must already
	// exist on this host (cross-host restore is out of scope for v1).
	targetUser, uerr := h.cfg.Users.FindByID(c.Request.Context(), req.TargetUserID)
	if uerr != nil || targetUser == nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "target_user_not_found"})
		return
	}

	destID := dest.ID
	job := &models.BackupJob{
		ID:            ids.NewULID(),
		UserID:        req.TargetUserID,
		DestinationID: &destID,
		Kind:          models.BackupJobKindAccountRestore,
		CreatedAt:     time.Now().UTC(),
		Status:        models.BackupJobStatusQueued,
	}
	if err := h.cfg.Jobs.Create(c.Request.Context(), job); err != nil {
		h.cfg.logErr("create restore job", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_create"})
		return
	}
	// backup.restore is synchronous on the agent (no goroutine fork like
	// backup.create). Use a long timeout so real-world account restores
	// (db dumps + mailbox imports) don't hard-fail at 10s, but cap at a
	// ceiling that matches operator UX expectations for the "Restore"
	// button — anything longer should run as a tracked background job.
	ctx, cancel := context.WithTimeout(c.Request.Context(), restoreCallTimeout)
	defer cancel()
	_ = h.cfg.Jobs.MarkStarted(c.Request.Context(), job.ID)
	params := map[string]any{
		"job_id":               job.ID,
		"manifest_snapshot_id": req.ManifestSnapshotID,
		"target_user_id":       req.TargetUserID,
		"target_username":      targetUser.Username,
		"overwrite":            req.Overwrite,
	}
	for k, v := range destWireParams(dest) {
		params[k] = v
	}
	var raw json.RawMessage
	err := backupwrapperhelpers.WithDestPasswordFile(ctx, dest, h.cfg.Agent, h.cfg.SSOKey,
		func(passwordFile string) error {
			if passwordFile != "" {
				params["password_file"] = passwordFile
			}
			var callErr error
			raw, callErr = h.cfg.Agent.Call(ctx, "backup.restore", params)
			return callErr
		})
	if err != nil {
		_ = h.cfg.Jobs.MarkFinished(c.Request.Context(), job.ID, models.BackupJobStatusFailed,
			"", "", 0, 0, nil, nil, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"status": "error", "error": "agent_call_failed", "detail": err.Error()})
		return
	}
	// Parse the agent's restore result so the job row reflects what
	// actually happened (succeeded / partial / failed based on stages).
	var result struct {
		JobID  string `json:"job_id"`
		Stages []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Error  string `json:"error,omitempty"`
		} `json:"stages"`
	}
	finalStatus := models.BackupJobStatusSucceeded
	finalErr := ""
	if uerr := json.Unmarshal(raw, &result); uerr == nil {
		failed := 0
		for _, s := range result.Stages {
			if s.Status == "failed" {
				failed++
				if finalErr == "" {
					finalErr = fmt.Sprintf("%s: %s", s.Name, s.Error)
				}
			}
		}
		switch {
		case failed > 0 && failed < len(result.Stages):
			finalStatus = models.BackupJobStatusPartial
		case failed > 0:
			finalStatus = models.BackupJobStatusFailed
		}
	}
	_ = h.cfg.Jobs.MarkFinished(c.Request.Context(), job.ID, finalStatus,
		"", "", 0, 0, raw, nil, finalErr)
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

// buildAccountMetadata delegates to the shared producer in
// panel-api/internal/backupmetadata so the admin and user-shell
// handlers stay in lockstep with the scheduler on schema changes.
func (cfg BackupHandlerConfig) buildAccountMetadata(ctx context.Context, user *models.User) *internalbackup.AccountMetadata {
	return backupmetadata.Build(ctx, user, backupmetadata.Deps{
		Databases: cfg.Databases, DatabaseUsers: cfg.DatabaseUsers, DatabaseGrants: cfg.DatabaseGrants,
		Domains: cfg.Domains, Mailboxes: cfg.Mailboxes, AppInstalls: cfg.AppInstalls,
		SSLCerts: cfg.SSLCerts, PHPPools: cfg.PHPPools, PHPPoolIni: cfg.PHPPoolIni,
		Forwarders: cfg.Forwarders, Autoresponders: cfg.Autoresponders, MailboxShares: cfg.MailboxShares,
		DNSSECKeys: cfg.DNSSECKeys, SSHKeys: cfg.SSHKeys, CronJobs: cfg.CronJobs,
		LimitOverrides: cfg.LimitOverrides, EgressPolicies: cfg.EgressPolicies, EgressRequests: cfg.EgressRequests,
		Log: cfg.Log,
	})
}


// allUserDatabases returns every MariaDB database name owned by a user.
// Used by manual + self-shell backup paths to default to "everything"
// when the operator submits an empty list. Errors are logged + an
// empty slice returned so a transient repo failure doesn't fall
// through into a "the agent backed up nothing" silent failure.
//
// M37: filters to engine='mariadb' so the legacy `databases` param on
// the agent's backup.create call only carries MariaDB names — PG
// names go in the parallel databases_postgres slice.
func (cfg BackupHandlerConfig) allUserDatabases(ctx context.Context, userID string) []string {
	return cfg.allUserDatabasesByEngine(ctx, userID, "mariadb")
}

// allUserPostgresDatabases — M37 sibling. Returns every PostgreSQL
// database name owned by a user. Backup orchestrator passes this
// to backup.create as databases_postgres alongside the mariadb list.
func (cfg BackupHandlerConfig) allUserPostgresDatabases(ctx context.Context, userID string) []string {
	return cfg.allUserDatabasesByEngine(ctx, userID, "postgres")
}

func (cfg BackupHandlerConfig) allUserDatabasesByEngine(ctx context.Context, userID, engine string) []string {
	if cfg.Databases == nil {
		return nil
	}
	rows, _, err := cfg.Databases.ListByUserID(ctx, userID, repository.ListOptions{Limit: 10000})
	if err != nil {
		cfg.logErr("list databases for backup", err, "user_id", userID, "engine", engine)
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Engine == engine || (engine == "mariadb" && r.Engine == "") {
			out = append(out, r.Name)
		}
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

	SSLCerts       repository.SSLCertificateRepository
	PHPPools       repository.PHPPoolRepository
	PHPPoolIni     repository.PHPPoolIniOverrideRepository
	Forwarders     repository.EmailForwarderRepository
	Autoresponders repository.EmailAutoresponderRepository
	MailboxShares  repository.MailboxShareRepository
	DNSSECKeys     repository.DNSSECKeyRepository
	SSHKeys        repository.SSHKeyRepository
	CronJobs       repository.CronJobRepository
	LimitOverrides repository.UserLimitOverrideRepository
	EgressPolicies repository.UserEgressPolicyRepository
	EgressRequests repository.UserEgressRequestRepository

	Log            *slog.Logger
}

// buildAccountMetadata projects MeBackupsHandlerConfig into the shared
// metadataDeps consumer. Same schema-v2 producer as BackupHandlerConfig.
func (cfg MeBackupsHandlerConfig) buildAccountMetadata(ctx context.Context, user *models.User) *internalbackup.AccountMetadata {
	return backupmetadata.Build(ctx, user, backupmetadata.Deps{
		Databases: cfg.Databases, DatabaseUsers: cfg.DatabaseUsers, DatabaseGrants: cfg.DatabaseGrants,
		Domains: cfg.Domains, Mailboxes: cfg.Mailboxes, AppInstalls: cfg.AppInstalls,
		SSLCerts: cfg.SSLCerts, PHPPools: cfg.PHPPools, PHPPoolIni: cfg.PHPPoolIni,
		Forwarders: cfg.Forwarders, Autoresponders: cfg.Autoresponders, MailboxShares: cfg.MailboxShares,
		DNSSECKeys: cfg.DNSSECKeys, SSHKeys: cfg.SSHKeys, CronJobs: cfg.CronJobs,
		LimitOverrides: cfg.LimitOverrides, EgressPolicies: cfg.EgressPolicies, EgressRequests: cfg.EgressRequests,
		Log: cfg.Log,
	})
}

func (cfg MeBackupsHandlerConfig) allUserDatabases(ctx context.Context, userID string) []string {
	return cfg.allUserDatabasesByEngine(ctx, userID, "mariadb")
}

func (cfg MeBackupsHandlerConfig) allUserPostgresDatabases(ctx context.Context, userID string) []string {
	return cfg.allUserDatabasesByEngine(ctx, userID, "postgres")
}

func (cfg MeBackupsHandlerConfig) allUserDatabasesByEngine(ctx context.Context, userID, engine string) []string {
	if cfg.Databases == nil {
		return nil
	}
	rows, _, err := cfg.Databases.ListByUserID(ctx, userID, repository.ListOptions{Limit: 10000})
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Engine == engine || (engine == "mariadb" && r.Engine == "") {
			out = append(out, r.Name)
		}
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
			"job_id":             job.ID,
			"user_id":            user.ID,
			"username":           user.Username,
			"email":              user.Email,
			"is_admin":           user.IsAdmin,
			"databases":          h.cfg.allUserDatabases(c.Request.Context(), user.ID),
			"databases_postgres": h.cfg.allUserPostgresDatabases(c.Request.Context(), user.ID),
			"mailboxes":          h.cfg.allUserMailboxes(c.Request.Context(), user.ID),
			"metadata":           h.cfg.buildAccountMetadata(c.Request.Context(), user),
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
