// M30.1 Step 7 — admin REST for backup_destinations (ADR-0078).
// Credentials are stored in env files at /etc/jabali-panel/restic-remotes/<id>.env
// (root:root 0600). Create/PATCH accept a `credentials_env` map in the
// request body which the handler writes to disk and pins to
// `credentials_ref`. GET responses NEVER return the env contents — only
// the file pointer + redacted preview ("AWS_ACCESS_KEY_ID=AKI...***").
package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	internalbackup "git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// CredentialsDir is the on-disk location for restic-remote env files.
// install.sh provisions /etc/jabali-panel/restic-remotes/ at root:root
// 0700; per-file mode is 0600.
const CredentialsDir = "/etc/jabali-panel/restic-remotes"

type BackupDestinationsConfig struct {
	Repo repository.BackupDestinationRepository
}

func RegisterBackupDestinationRoutes(rg *gin.RouterGroup, cfg BackupDestinationsConfig) {
	if cfg.Repo == nil {
		return
	}
	h := &backupDestinationHandler{repo: cfg.Repo}
	admin := rg.Group("/admin", middleware.RequireAdmin())
	admin.GET("/backup-destinations", h.list)
	admin.GET("/backup-destinations/:id", h.get)
	admin.POST("/backup-destinations", h.create)
	admin.PATCH("/backup-destinations/:id", h.update)
	admin.DELETE("/backup-destinations/:id", h.delete)
	admin.POST("/backup-destinations/:id/test", h.test)
}

type backupDestinationHandler struct {
	repo repository.BackupDestinationRepository
}

type backupDestinationDTO struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	Kind                string  `json:"kind"`
	URL                 string  `json:"url"`
	HasCredentials      bool    `json:"has_credentials"`
	CredentialsRef      *string `json:"credentials_ref,omitempty"`
	CredentialsKeysMask []string `json:"credentials_keys_mask,omitempty"`
	Enabled             bool    `json:"enabled"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
}

func toDestDTO(d *models.BackupDestination) backupDestinationDTO {
	dto := backupDestinationDTO{
		ID:        d.ID,
		Name:      d.Name,
		Kind:      d.Kind,
		URL:       d.URL,
		Enabled:   d.Enabled,
		CreatedAt: d.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: d.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if d.CredentialsRef != nil && *d.CredentialsRef != "" {
		dto.HasCredentials = true
		dto.CredentialsRef = d.CredentialsRef
		// Read just the keys (no values) for the UI to display
		// "stored credentials: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY".
		if env, err := internalbackup.LoadEnvFile(*d.CredentialsRef); err == nil {
			for _, line := range env {
				if i := strings.Index(line, "="); i > 0 {
					dto.CredentialsKeysMask = append(dto.CredentialsKeysMask, line[:i])
				}
			}
		}
	}
	return dto
}

func (h *backupDestinationHandler) list(c *gin.Context) {
	rows, err := h.repo.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_list"})
		return
	}
	out := make([]backupDestinationDTO, 0, len(rows))
	for i := range rows {
		out = append(out, toDestDTO(&rows[i]))
	}
	c.JSON(http.StatusOK, gin.H{"data": out, "total": len(out)})
}

func (h *backupDestinationHandler) get(c *gin.Context) {
	d, err := h.repo.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_get"})
		return
	}
	c.JSON(http.StatusOK, toDestDTO(d))
}

type createDestinationRequest struct {
	Name           string            `json:"name"            binding:"required"`
	Kind           string            `json:"kind"            binding:"required"`
	URL            string            `json:"url"             binding:"required"`
	Enabled        *bool             `json:"enabled"`
	CredentialsEnv map[string]string `json:"credentials_env"`
}

func (h *backupDestinationHandler) create(c *gin.Context) {
	var req createDestinationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_body", "detail": err.Error()})
		return
	}
	if !validKind(req.Kind) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_kind"})
		return
	}
	if err := validateURLForKind(req.Kind, req.URL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_url", "detail": err.Error()})
		return
	}
	id := ids.NewULID()
	d := &models.BackupDestination{
		ID:      id,
		Name:    strings.TrimSpace(req.Name),
		Kind:    req.Kind,
		URL:     req.URL,
		Enabled: true,
	}
	if req.Enabled != nil {
		d.Enabled = *req.Enabled
	}
	if len(req.CredentialsEnv) > 0 {
		path, err := writeCredentialsEnvFile(id, req.CredentialsEnv)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "creds_write_failed", "detail": err.Error()})
			return
		}
		d.CredentialsRef = &path
	}
	if err := h.repo.Create(c.Request.Context(), d); err != nil {
		// Roll back creds file on DB failure.
		if d.CredentialsRef != nil {
			_ = os.Remove(*d.CredentialsRef)
		}
		if errors.Is(err, repository.ErrConflict) {
			c.JSON(http.StatusConflict, gin.H{"status": "error", "error": "name_in_use"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_create"})
		return
	}
	c.JSON(http.StatusCreated, toDestDTO(d))
}

type updateDestinationRequest struct {
	Name           *string           `json:"name"`
	Kind           *string           `json:"kind"`
	URL            *string           `json:"url"`
	Enabled        *bool             `json:"enabled"`
	CredentialsEnv map[string]string `json:"credentials_env"` // empty map = no change; nil = clear
	ClearCreds     bool              `json:"clear_credentials"`
}

func (h *backupDestinationHandler) update(c *gin.Context) {
	id := c.Param("id")
	d, err := h.repo.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_get"})
		return
	}
	var req updateDestinationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_body", "detail": err.Error()})
		return
	}
	if req.Name != nil {
		d.Name = strings.TrimSpace(*req.Name)
	}
	if req.Kind != nil {
		if !validKind(*req.Kind) {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_kind"})
			return
		}
		d.Kind = *req.Kind
	}
	if req.URL != nil {
		if err := validateURLForKind(d.Kind, *req.URL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_url", "detail": err.Error()})
			return
		}
		d.URL = *req.URL
	}
	if req.Enabled != nil {
		d.Enabled = *req.Enabled
	}
	if req.ClearCreds {
		if d.CredentialsRef != nil {
			_ = os.Remove(*d.CredentialsRef)
		}
		d.CredentialsRef = nil
	}
	if len(req.CredentialsEnv) > 0 {
		path, err := writeCredentialsEnvFile(d.ID, req.CredentialsEnv)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "creds_write_failed", "detail": err.Error()})
			return
		}
		d.CredentialsRef = &path
	}
	if err := h.repo.Update(c.Request.Context(), d); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_update"})
		return
	}
	c.JSON(http.StatusOK, toDestDTO(d))
}

func (h *backupDestinationHandler) delete(c *gin.Context) {
	id := c.Param("id")
	d, err := h.repo.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_get"})
		return
	}
	if err := h.repo.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_delete"})
		return
	}
	if d.CredentialsRef != nil {
		_ = os.Remove(*d.CredentialsRef)
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// test runs `restic snapshots --json` against the destination using
// the stored creds. Used by the admin "Test" button on the destination
// drawer. Returns the snapshot count on success; on failure returns
// the restic error verbatim so the operator can fix bad creds.
func (h *backupDestinationHandler) test(c *gin.Context) {
	id := c.Param("id")
	d, err := h.repo.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "db_get"})
		return
	}
	if d.Kind == models.BackupDestinationKindLocal {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "detail": "local destination — no remote test"})
		return
	}
	var extra []string
	if d.CredentialsRef != nil && *d.CredentialsRef != "" {
		extra, err = internalbackup.LoadEnvFile(*d.CredentialsRef)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "creds_load_failed", "detail": err.Error()})
			return
		}
	}
	stdout, stderr, err := internalbackup.SnapshotsRemote(
		c.Request.Context(),
		nil,
		d.URL,
		internalbackup.DefaultPasswordFile,
		extra,
	)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"status": "error",
			"error":  "restic_test_failed",
			"detail": err.Error(),
			"stderr": strings.TrimSpace(string(stderr)),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"stdout_preview": firstLine(string(stdout)),
	})
}

func validKind(kind string) bool {
	for _, k := range models.AllBackupDestinationKinds {
		if k == kind {
			return true
		}
	}
	return false
}

func validateURLForKind(kind, url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return errors.New("url required")
	}
	switch kind {
	case models.BackupDestinationKindLocal:
		if !strings.HasPrefix(url, "/") {
			return errors.New("local URL must be an absolute path")
		}
	case models.BackupDestinationKindSFTP:
		if !strings.HasPrefix(url, "sftp:") {
			return errors.New("sftp URL must start with 'sftp:'")
		}
	case models.BackupDestinationKindS3:
		if !strings.HasPrefix(url, "s3:") {
			return errors.New("s3 URL must start with 's3:'")
		}
	case models.BackupDestinationKindB2:
		if !strings.HasPrefix(url, "b2:") {
			return errors.New("b2 URL must start with 'b2:'")
		}
	case models.BackupDestinationKindAzure:
		if !strings.HasPrefix(url, "azure:") {
			return errors.New("azure URL must start with 'azure:'")
		}
	case models.BackupDestinationKindGCS:
		if !strings.HasPrefix(url, "gs:") {
			return errors.New("gcs URL must start with 'gs:'")
		}
	case models.BackupDestinationKindREST:
		if !strings.HasPrefix(url, "rest:") {
			return errors.New("rest URL must start with 'rest:'")
		}
	}
	return nil
}

// writeCredentialsEnvFile writes <CredentialsDir>/<destID>.env atomically
// as 0600 root:root. Caller is responsible for cleaning up the file on
// rollback. Existing file is replaced via rename().
func writeCredentialsEnvFile(destID string, env map[string]string) (string, error) {
	if err := os.MkdirAll(CredentialsDir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", CredentialsDir, err)
	}
	final := filepath.Join(CredentialsDir, destID+".env")
	tmp, err := os.CreateTemp(CredentialsDir, destID+".env.*")
	if err != nil {
		return "", fmt.Errorf("create temp env: %w", err)
	}
	cleanup := func() { _ = os.Remove(tmp.Name()) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", fmt.Errorf("chmod env: %w", err)
	}
	for k, v := range env {
		if !validEnvKey(k) {
			_ = tmp.Close()
			cleanup()
			return "", fmt.Errorf("invalid env key %q", k)
		}
		if strings.ContainsAny(v, "\n\r") {
			_ = tmp.Close()
			cleanup()
			return "", fmt.Errorf("env value for %q contains newline", k)
		}
		if _, err := fmt.Fprintf(tmp, "%s=%s\n", k, v); err != nil {
			_ = tmp.Close()
			cleanup()
			return "", fmt.Errorf("write env: %w", err)
		}
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", fmt.Errorf("close env: %w", err)
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		cleanup()
		return "", fmt.Errorf("rename env: %w", err)
	}
	return final, nil
}

func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r >= 'A' && r <= 'Z':
		case r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}
