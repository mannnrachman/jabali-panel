// M35 admin REST endpoints — read-only for now (list/get/stages).
// Mutation endpoints (create/cancel/resume) land alongside the
// admin UI Step 8 once the JMAP-push + CreateUser orchestrator
// pieces stabilise. v1 ships read-only because operators can
// already create + run jobs via the cobra CLI (jabali migrate
// import); the REST adds a UI-friendly observation surface.
//
// Routes mounted under /admin/migrations:
//   GET    /admin/migrations                  list (paginated envelope)
//   POST   /admin/migrations                  create (state=pending; runner
//                                             stays operator-driven via CLI)
//   GET    /admin/migrations/:id              one job + recent stages
//   GET    /admin/migrations/:id/stages       full stage timeline
//   DELETE /admin/migrations/:id              soft-revoke (state→cancelled)
//   POST   /admin/migrations/:id/destroy      hard-delete row + secret + extracted dir
//                                             (requires terminal state)
//
// Admin-gated. RequireAdmin already mounted by the parent group.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// AdminMigrationsHandlerConfig is the dep set the routes need.
// Jobs is required; nil disables route registration. Agent is
// required for the three drive endpoints (secrets / pull-source /
// import); when nil those three endpoints register but 503 because
// the agent socket isn't reachable.
type AdminMigrationsHandlerConfig struct {
	Jobs  repository.MigrationJobRepository
	Agent agent.AgentInterface
}

// RegisterAdminMigrationRoutes mounts /admin/migrations* on g.
// g must already enforce RequireAuth + RequireAdmin upstream.
func RegisterAdminMigrationRoutes(g *gin.RouterGroup, cfg AdminMigrationsHandlerConfig) {
	if cfg.Jobs == nil {
		return
	}
	h := &adminMigrationsHandler{cfg: cfg}
	rg := g.Group("/admin/migrations")
	rg.Use(middleware.RequireAdmin())
	rg.GET("", h.list)
	rg.POST("", h.create)
	rg.GET("/:id", h.get)
	rg.GET("/:id/stages", h.stages)
	rg.DELETE("/:id", h.cancel)
	rg.POST("/:id/destroy", h.destroy)
	// SPA-driven migration endpoints. Each writes/launches via the
	// agent (transient systemd unit pattern, M29 §updates) so the
	// long-running pull + import survive panel-api restarts.
	rg.POST("/:id/secrets", h.uploadSecrets)
	rg.POST("/:id/pull-source", h.runPullSource)
	rg.POST("/:id/import", h.runImport)
	// WHM pkgacct (offline) flow: operator uploads a pre-built tarball
	// rather than pulling over SSH. Streams directly into the staging
	// directory so multi-GB pkgacct dumps don't double-buffer through
	// /tmp.
	rg.POST("/:id/tarball", h.uploadTarball)
	rg.GET("/:id/tarball", h.tarballStatus)
}

type adminMigrationsHandler struct{ cfg AdminMigrationsHandlerConfig }

type migrationJobListResponse struct {
	Data     []models.MigrationJob `json:"data"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

func (h *adminMigrationsHandler) list(c *gin.Context) {
	page, pageSize := paginationParams(c)
	rows, total, err := h.cfg.Jobs.List(c.Request.Context(), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if rows == nil {
		rows = []models.MigrationJob{}
	}
	c.JSON(http.StatusOK, migrationJobListResponse{
		Data: rows, Total: total, Page: page, PageSize: pageSize,
	})
}

// migrationJobDetailResponse bundles the job header + the most
// recent stage rows so the UI can render the timeline without
// a follow-up call.
type migrationJobDetailResponse struct {
	Job    *models.MigrationJob     `json:"job"`
	Stages []models.MigrationStage  `json:"stages"`
}

func (h *adminMigrationsHandler) get(c *gin.Context) {
	job, err := h.cfg.Jobs.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	stages, err := h.cfg.Jobs.ListStages(c.Request.Context(), job.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if stages == nil {
		stages = []models.MigrationStage{}
	}
	c.JSON(http.StatusOK, migrationJobDetailResponse{Job: job, Stages: stages})
}

func (h *adminMigrationsHandler) stages(c *gin.Context) {
	stages, err := h.cfg.Jobs.ListStages(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if stages == nil {
		stages = []models.MigrationStage{}
	}
	c.JSON(http.StatusOK, gin.H{"data": stages, "total": len(stages)})
}

// createMigrationRequest is the wire shape for POST /admin/migrations.
type createMigrationRequest struct {
	SourceKind string `json:"source_kind" binding:"required"`
	SourceHost string `json:"source_host"`
	SourceUser string `json:"source_user" binding:"required"`
}

// create inserts a fresh migration_jobs row with state='pending'.
// Does NOT kick off the runner — runner stays operator-driven via
// the cobra CLI ('jabali migrate import') so a misclick on the SPA
// can't trigger a 30-min restore against an unprepared destination.
//
// Validates source_kind against the registered importers + the
// offline whm_pkgacct kind so the operator can't create a row
// for an unknown kind that the runner would later reject.
func (h *adminMigrationsHandler) create(c *gin.Context) {
	var req createMigrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	if !isKnownSourceKind(req.SourceKind) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "unknown_source_kind",
			"detail": "supported kinds: cpanel, whm_pkgacct, directadmin, hestiacp, imap_only",
		})
		return
	}
	// Refuse duplicates on the natural-key tuple — the UNIQUE on
	// (source_host, source_user, source_kind) would 500 the
	// gorm.Create otherwise.
	if existing, _ := h.cfg.Jobs.FindBySource(c.Request.Context(),
		req.SourceKind, req.SourceHost, req.SourceUser); existing != nil {
		c.JSON(http.StatusConflict, gin.H{
			"error":          "duplicate_migration_job",
			"existing_job_id": existing.ID,
			"detail":         "A migration job already exists for this (source_host, source_user, source_kind). Resume via 'jabali migrate import --job-id ...' instead of recreating.",
		})
		return
	}

	row := &models.MigrationJob{
		ID:         genULID(),
		SourceKind: req.SourceKind,
		SourceHost: req.SourceHost,
		SourceUser: req.SourceUser,
		State:      models.MigrationStatePending,
	}
	if err := h.cfg.Jobs.Create(c.Request.Context(), row); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal", "detail": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, row)
}

// isKnownSourceKind gates against the closed set of source-kinds
// the runner currently understands. Importer registry would also
// answer this, but offline kinds (whm_pkgacct) aren't registered
// — explicit allow-list is clearer.
func isKnownSourceKind(s string) bool {
	switch s {
	case models.MigrationSourceCpanel,
		models.MigrationSourceWHMpkgacct,
		models.MigrationSourceDirectAdmin,
		models.MigrationSourceHestia,
		models.MigrationSourceIMAPOnly:
		return true
	}
	return false
}

// genULID is hoisted so tests can swap in a deterministic ULID
// generator without monkey-patching ids.NewULID at package level.
var genULID = ulidNew

// cancel soft-revokes a job — transitions to MigrationStateCancelled
// when the current state allows. Already-terminal jobs (done /
// failed / cancelled) return 409 so the operator knows the row was
// never advanced.
//
// NOTE: cancel does NOT kill an in-flight `jabali migrate import`
// process. v1 transient-unit invoker is the operator's cobra
// shell; cancelling at the panel layer just stamps the DB row.
// A follow-up agent-level kill switch is M35 Step 8 follow-up.
func (h *adminMigrationsHandler) cancel(c *gin.Context) {
	id := c.Param("id")
	job, err := h.cfg.Jobs.FindByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !migrate.IsValidJobTransition(job.State, models.MigrationStateCancelled) {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "illegal_transition",
			"detail": "job in state '" + job.State + "' cannot be cancelled (already terminal?)",
		})
		return
	}
	reason := "cancelled by admin"
	if err := h.cfg.Jobs.UpdateState(c.Request.Context(), id, models.MigrationStateCancelled, &reason); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	// Same immediate-secrets-wipe as the runner's terminal paths
	// (ADR-0094). Best-effort: failure to wipe surfaces in the
	// daily reaper's next sweep.
	_ = migrate.WipeJobSecret(id)
	c.JSON(http.StatusOK, gin.H{"id": id, "state": models.MigrationStateCancelled})
}

// destroy hard-deletes a terminal-state migration_jobs row + wipes
// the secrets file + removes /var/lib/jabali-migrations/<id>/.
// Refuses non-terminal jobs to prevent accidental destruction of an
// in-flight migration.
//
// Three side effects on success:
//   - migration_jobs row deleted (FK CASCADE drops migration_stages)
//   - /etc/jabali-panel/migration-secrets/<id>.env removed if present
//   - /var/lib/jabali-migrations/<id>/ removed (extracted tarball,
//     downloaded source tar, etc.)
//
// Each is best-effort: failure to wipe the FS dir doesn't roll back
// the DB delete (operator can rm -rf the dir manually). Failure to
// delete the DB row does fail the request.
func (h *adminMigrationsHandler) destroy(c *gin.Context) {
	id := c.Param("id")
	job, err := h.cfg.Jobs.FindByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	switch job.State {
	case models.MigrationStateDone, models.MigrationStateFailed, models.MigrationStateCancelled:
		// allowed
	default:
		c.JSON(http.StatusConflict, gin.H{
			"error":  "non_terminal",
			"detail": "destroy refused: cancel the job first (DELETE /admin/migrations/" + id + ") to transition to terminal state",
		})
		return
	}
	// DB row first — FK CASCADE drops migration_stages.
	if err := h.cfg.Jobs.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal", "detail": err.Error()})
		return
	}
	// Filesystem side-effects best-effort. RemoveAll is OK to call
	// on a non-existent path; doesn't error.
	_ = migrate.WipeJobSecret(id)
	stagingDir := "/var/lib/jabali-migrations/" + id
	_ = os.RemoveAll(stagingDir)
	c.JSON(http.StatusOK, gin.H{"id": id, "destroyed": true})
}

// paginationParams pulls ?page= + ?page_size= with sane defaults.
// Reused shape from M44 + M30 admin endpoints; not extracted into
// a shared helper because each handler tunes its own caps.
func paginationParams(c *gin.Context) (page, pageSize int) {
	page = 1
	pageSize = 50
	if v := c.Query("page"); v != "" {
		if n, err := atoiNonNeg(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.Query("page_size"); v != "" {
		if n, err := atoiNonNeg(v); err == nil && n > 0 && n <= 200 {
			pageSize = n
		}
	}
	return page, pageSize
}

// ulidNew defaults genULID to the panel's canonical ULID generator.
// Kept as a package-level var (rather than a direct ids.NewULID
// reference inline) so test paths can override.
func ulidNew() string {
	return ids.NewULID()
}

// uploadSecretsRequest — operator picks ONE of password / private-key
// per ADR-0094. UI presents a tabbed form; either tab posts here.
type uploadSecretsRequest struct {
	SSHPassword   string `json:"ssh_password"`
	SSHPrivateKey string `json:"ssh_private_key"`
}

// uploadSecrets — POST /admin/migrations/:id/secrets writes the per-job
// env file via the agent (root-owned 0640). Job must exist + be in a
// non-terminal state. Empty payload (neither password nor key) → 400.
func (h *adminMigrationsHandler) uploadSecrets(c *gin.Context) {
	id := c.Param("id")
	job, err := h.cfg.Jobs.FindByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if isTerminalMigrationState(job.State) {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "terminal_state",
			"detail": "cannot upload secrets to a terminal job (state=" + job.State + ")",
		})
		return
	}
	var req uploadSecretsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	if req.SSHPassword == "" && req.SSHPrivateKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "missing_credential",
			"detail": "ssh_password or ssh_private_key required",
		})
		return
	}
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_unconfigured"})
		return
	}
	h.callAgent(c, "migration.secrets_write", map[string]any{
		"job_id":          id,
		"ssh_password":    req.SSHPassword,
		"ssh_private_key": req.SSHPrivateKey,
	}, 10*time.Second)
}

// runPullSourceRequest — defaults: ssh_user="root".
type runPullSourceRequest struct {
	SSHUser string `json:"ssh_user"`
}

// runPullSource — POST /admin/migrations/:id/pull-source kicks off
// `jabali migrate pull-source --job-id` under a transient systemd
// unit. Refuses if the job has no manifest (manifest = source-side
// discovery output; pull-source assumes discovery already ran). State
// must be pending or pulling_failed.
func (h *adminMigrationsHandler) runPullSource(c *gin.Context) {
	id := c.Param("id")
	job, err := h.cfg.Jobs.FindByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if isTerminalMigrationState(job.State) {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "terminal_state",
			"detail": "cannot pull a terminal job (state=" + job.State + ")",
		})
		return
	}
	var req runPullSourceRequest
	_ = c.ShouldBindJSON(&req) // body optional
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_unconfigured"})
		return
	}
	h.callAgent(c, "migration.pull_source_run", map[string]any{
		"job_id":   id,
		"ssh_user": req.SSHUser,
	}, 10*time.Second)
}

// runImportRequest — TargetUser is required; the rest are optional.
// When TargetEmail/TargetPassword are present + the user doesn't yet
// exist, the import command auto-creates them (per M35 auto-create
// flow added in 91ba51a9).
type runImportRequest struct {
	TargetUser      string `json:"target_user" binding:"required"`
	TargetEmail     string `json:"target_email"`
	TargetPassword  string `json:"target_password"`
	TargetPackageID string `json:"target_package_id"`
}

func (h *adminMigrationsHandler) runImport(c *gin.Context) {
	id := c.Param("id")
	job, err := h.cfg.Jobs.FindByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if isTerminalMigrationState(job.State) {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "terminal_state",
			"detail": "cannot import a terminal job (state=" + job.State + ")",
		})
		return
	}
	var req runImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_unconfigured"})
		return
	}
	h.callAgent(c, "migration.import_run", map[string]any{
		"job_id":            id,
		"target_user":       req.TargetUser,
		"target_email":      req.TargetEmail,
		"target_password":   req.TargetPassword,
		"target_package_id": req.TargetPackageID,
	}, 10*time.Second)
}

// callAgent — copy of admin_updates.go pattern. Hoisted as a method
// so both add a context-deadline + uniform error envelope.
func (h *adminMigrationsHandler) callAgent(c *gin.Context, cmd string, params any, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	raw, err := h.cfg.Agent.Call(ctx, cmd, params)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_error", "details": err.Error()})
		return
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_parse"})
		return
	}
	c.JSON(http.StatusOK, data)
}

// isTerminalMigrationState — done / failed / cancelled. Prevents
// re-running pull or import on a terminal job; operator must
// destroy + recreate.
func isTerminalMigrationState(s string) bool {
	switch s {
	case models.MigrationStateDone, models.MigrationStateFailed, models.MigrationStateCancelled:
		return true
	}
	return false
}

// atoiNonNeg is strconv.Atoi with a non-negative refusal so a
// negative `page` doesn't underflow the offset calculation in the
// repo.
func atoiNonNeg(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("non-digit")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}


// migrationStagingDir returns the per-job staging dir used by both
// SSH-pull (jabali migrate pull-source) and the offline tarball-
// upload paths. Mirrored from migrate_pull_cmd.go so the SPA hits
// the same convention the cobra runner expects.
func migrationStagingDir(jobID string) string {
	return "/var/lib/jabali-migrations/" + jobID
}

// uploadTarball — POST /admin/migrations/:id/tarball streams a
// multipart/form-data 'file' part directly into the staging dir as
// source.tar.gz (root:jabali-readable, panel-api owns the dir per
// install.sh:2849). Streams via MultipartReader rather than
// ParseMultipartForm so a multi-GB pkgacct dump doesn't double-
// buffer through /tmp.
//
// Wire shape:
//
//	POST /admin/migrations/<id>/tarball
//	Content-Type: multipart/form-data; boundary=...
//	file: <pkgacct-cpmove-foo.tar.gz>
//
// Response: {path, size_bytes, sha256_truncated}.
//
// Refuses if job is in a terminal state OR an existing tarball is
// already present (operator must DELETE /admin/migrations/<id> +
// recreate, or destroy the job, to retry — re-uploading on top of
// an extracted source dir would fight the runner).
func (h *adminMigrationsHandler) uploadTarball(c *gin.Context) {
	id := c.Param("id")
	job, err := h.cfg.Jobs.FindByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if isTerminalMigrationState(job.State) {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "terminal_state",
			"detail": "cannot upload tarball to a terminal job (state=" + job.State + ")",
		})
		return
	}
	staging := migrationStagingDir(id)
	if err := os.MkdirAll(staging, 0o750); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "mkdir_staging", "detail": err.Error()})
		return
	}
	target := filepath.Join(staging, "source.tar.gz")
	if _, err := os.Stat(target); err == nil {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "tarball_exists",
			"detail": "destroy the job (POST /admin/migrations/" + id + "/destroy after cancel) to clear the staging dir before re-uploading",
		})
		return
	}

	// Streaming upload — MultipartReader returns parts one-by-one so
	// nothing buffers in memory or /tmp. 20 GiB cap on Body matches
	// the upper bound of a typical cPanel full-backup tarball; smaller
	// pkgacct dumps stream the same way.
	const maxUploadBytes = 20 * 1024 * 1024 * 1024
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)
	reader, err := c.Request.MultipartReader()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "not_multipart", "detail": err.Error()})
		return
	}
	var (
		wrote   int64
		wrotten = false
	)
	for {
		part, perr := reader.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "multipart_read", "detail": perr.Error()})
			return
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		tmp := target + ".part"
		dst, oerr := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
		if oerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "open_tmp", "detail": oerr.Error()})
			return
		}
		n, cerr := io.Copy(dst, part)
		_ = part.Close()
		if cerr != nil {
			_ = dst.Close()
			_ = os.Remove(tmp)
			// MaxBytesReader hit returns http: request body too large;
			// surface it as 413 so the client knows to split or compress.
			if cerr.Error() == "http: request body too large" {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"error":     "tarball_too_large",
					"max_bytes": maxUploadBytes,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "stream_failed", "detail": cerr.Error()})
			return
		}
		if err := dst.Close(); err != nil {
			_ = os.Remove(tmp)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "close_tmp", "detail": err.Error()})
			return
		}
		if err := os.Rename(tmp, target); err != nil {
			_ = os.Remove(tmp)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "rename", "detail": err.Error()})
			return
		}
		wrote = n
		wrotten = true
		break
	}
	if !wrotten {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_file_field", "detail": "expected one multipart part named 'file'"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"path":        target,
		"size_bytes":  wrote,
	})
}

// tarballStatus — GET /admin/migrations/:id/tarball reports whether a
// tarball is staged + its size. SPA uses this to drive the upload-vs-
// re-upload UI state on the detail page.
func (h *adminMigrationsHandler) tarballStatus(c *gin.Context) {
	id := c.Param("id")
	if _, err := h.cfg.Jobs.FindByID(c.Request.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	target := filepath.Join(migrationStagingDir(id), "source.tar.gz")
	st, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, gin.H{"present": false})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "stat", "detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"present":    true,
		"path":       target,
		"size_bytes": st.Size(),
		"mtime":      st.ModTime().UTC().Format(time.RFC3339),
	})
}
