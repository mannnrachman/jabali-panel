// M30.1 Step 7 — admin REST for backup_destinations (ADR-0078).
// Credentials are stored in env files at /etc/jabali-panel/restic-remotes/<id>.env
// (root:root 0600). Create/PATCH accept a `credentials_env` map in the
// request body which the handler writes to disk and pins to
// `credentials_ref`. GET responses NEVER return the env contents — only
// the file pointer + redacted preview ("AWS_ACCESS_KEY_ID=AKI...***").
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	internalbackup "git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/backupwrapperhelpers"
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
	Repo  repository.BackupDestinationRepository
	Agent agent.AgentInterface
}

func RegisterBackupDestinationRoutes(rg *gin.RouterGroup, cfg BackupDestinationsConfig) {
	if cfg.Repo == nil {
		return
	}
	h := &backupDestinationHandler{repo: cfg.Repo, agent: cfg.Agent}
	admin := rg.Group("/admin", middleware.RequireAdmin())
	admin.GET("/backup-destinations", h.list)
	admin.GET("/backup-destinations/:id", h.get)
	admin.POST("/backup-destinations", h.create)
	admin.PATCH("/backup-destinations/:id", h.update)
	admin.DELETE("/backup-destinations/:id", h.delete)
	admin.POST("/backup-destinations/:id/test", h.test)
}

type backupDestinationHandler struct {
	repo  repository.BackupDestinationRepository
	agent agent.AgentInterface
}

// writeCreds dispatches the env-file write to the agent (root). panel-api
// runs as the jabali user with ProtectSystem=strict and cannot touch
// /etc/jabali-panel/. Returns the on-disk path the agent wrote.
func (h *backupDestinationHandler) writeCreds(ctx context.Context, destID string, env map[string]string) (string, error) {
	if h.agent == nil {
		return "", errors.New("agent unavailable")
	}
	raw, err := h.agent.Call(ctx, "backup.dest.creds_write", map[string]any{
		"dest_id": destID, "env": env,
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("parse creds_write reply: %w", err)
	}
	return resp.Path, nil
}

// deleteCreds dispatches the file removal to the agent. Best-effort —
// caller treats errors as non-fatal because the row is being deleted
// regardless.
func (h *backupDestinationHandler) deleteCreds(ctx context.Context, destID string) {
	if h.agent == nil {
		return
	}
	_, _ = h.agent.Call(ctx, "backup.dest.creds_delete", map[string]any{
		"dest_id": destID,
	})
}

type backupDestinationDTO struct {
	ID                  string                                `json:"id"`
	Name                string                                `json:"name"`
	Kind                string                                `json:"kind"`
	URL                 string                                `json:"url"`
	HasCredentials      bool                                  `json:"has_credentials"`
	CredentialsRef      *string                               `json:"credentials_ref,omitempty"`
	CredentialsKeysMask []string                              `json:"credentials_keys_mask,omitempty"`
	ExtraOptions        *models.BackupDestinationExtraOptions `json:"extra_options,omitempty"`
	Enabled             bool                                  `json:"enabled"`
	CreatedAt           string                                `json:"created_at"`
	UpdatedAt           string                                `json:"updated_at"`
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
	if len(d.ExtraOptions) > 0 {
		opts := d.ExtraOptionsTyped()
		dto.ExtraOptions = &opts
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
	URL            string            `json:"url"`
	Enabled        *bool             `json:"enabled"`
	CredentialsEnv map[string]string `json:"credentials_env"`
	// SFTP-only structured fields. When Kind == "sftp" the handler
	// composes URL + extra_options from these and ignores any URL the
	// client sent (UI doesn't expose the URL field for sftp).
	SFTP         *sftpRequestOptions `json:"sftp,omitempty"`
	SFTPPassword string              `json:"sftp_password,omitempty"` // plain — written to creds env as SSHPASS
}

type sftpRequestOptions struct {
	Host    string `json:"host"`
	User    string `json:"user"`
	Port    int    `json:"port,omitempty"`
	Path    string `json:"path"`
	Auth    string `json:"auth"`               // "key" | "password"
	KeyPath string `json:"key_path,omitempty"` // absolute path to private key
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

	id := ids.NewULID()

	// SFTP path: derive URL + extra_options + (optional) password env
	// from the structured req.SFTP block. Falls back to req.URL for
	// non-SFTP backends.
	var (
		composedURL  string
		extraOptions json.RawMessage
		credsEnv     = req.CredentialsEnv
	)
	if req.Kind == models.BackupDestinationKindSFTP {
		if req.SFTP == nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "sftp_block_required"})
			return
		}
		if err := validateSFTPInputs(req.SFTP); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_sftp", "detail": err.Error()})
			return
		}
		composedURL = internalbackup.ComposeSFTPURL(internalbackup.SFTPInputs{
			Host: req.SFTP.Host, User: req.SFTP.User, Path: req.SFTP.Path,
		})
		raw, _ := json.Marshal(models.BackupDestinationExtraOptions{
			SFTP: &models.SFTPOptions{
				Host: req.SFTP.Host, User: req.SFTP.User, Port: req.SFTP.Port,
				Path: req.SFTP.Path, Auth: req.SFTP.Auth, KeyPath: req.SFTP.KeyPath,
			},
		})
		extraOptions = raw
		if req.SFTP.Auth == models.SFTPAuthPassword {
			if req.SFTPPassword == "" {
				c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "sftp_password_required"})
				return
			}
			if credsEnv == nil {
				credsEnv = map[string]string{}
			}
			credsEnv["SSHPASS"] = req.SFTPPassword
		}
	} else {
		if err := validateURLForKind(req.Kind, req.URL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_url", "detail": err.Error()})
			return
		}
		composedURL = req.URL
	}

	d := &models.BackupDestination{
		ID:           id,
		Name:         strings.TrimSpace(req.Name),
		Kind:         req.Kind,
		URL:          composedURL,
		ExtraOptions: extraOptions,
		Enabled:      true,
	}
	if req.Enabled != nil {
		d.Enabled = *req.Enabled
	}
	if len(credsEnv) > 0 {
		path, err := h.writeCreds(c.Request.Context(), id, credsEnv)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "creds_write_failed", "detail": err.Error()})
			return
		}
		d.CredentialsRef = &path
	}
	if err := h.repo.Create(c.Request.Context(), d); err != nil {
		// Roll back creds file on DB failure.
		if d.CredentialsRef != nil {
			h.deleteCreds(c.Request.Context(), id)
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

// validateSFTPInputs enforces non-empty host+user, non-empty path, and
// auth ∈ {key, password}. Key path absolute when auth=key.
func validateSFTPInputs(s *sftpRequestOptions) error {
	if s.Host == "" || s.User == "" {
		return errors.New("host and user are required")
	}
	if s.Path == "" {
		return errors.New("path is required")
	}
	switch s.Auth {
	case models.SFTPAuthKey:
		if s.KeyPath != "" && !strings.HasPrefix(s.KeyPath, "/") {
			return errors.New("key_path must be an absolute path")
		}
	case models.SFTPAuthPassword:
		// password value lives in req.SFTPPassword
	case "":
		// blank = key auth using default ssh config
	default:
		return errors.New("auth must be 'key' or 'password'")
	}
	return nil
}

type updateDestinationRequest struct {
	Name           *string             `json:"name"`
	Kind           *string             `json:"kind"`
	URL            *string             `json:"url"`
	Enabled        *bool               `json:"enabled"`
	CredentialsEnv map[string]string   `json:"credentials_env"` // empty map = no change; nil = clear
	ClearCreds     bool                `json:"clear_credentials"`
	SFTP           *sftpRequestOptions `json:"sftp,omitempty"`
	SFTPPassword   string              `json:"sftp_password,omitempty"`
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
	if req.URL != nil && d.Kind != models.BackupDestinationKindSFTP {
		if err := validateURLForKind(d.Kind, *req.URL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_url", "detail": err.Error()})
			return
		}
		d.URL = *req.URL
	}
	if req.SFTP != nil {
		if d.Kind != models.BackupDestinationKindSFTP {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "sftp_block_only_for_sftp_kind"})
			return
		}
		if err := validateSFTPInputs(req.SFTP); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_sftp", "detail": err.Error()})
			return
		}
		d.URL = internalbackup.ComposeSFTPURL(internalbackup.SFTPInputs{
			Host: req.SFTP.Host, User: req.SFTP.User, Path: req.SFTP.Path,
		})
		raw, _ := json.Marshal(models.BackupDestinationExtraOptions{
			SFTP: &models.SFTPOptions{
				Host: req.SFTP.Host, User: req.SFTP.User, Port: req.SFTP.Port,
				Path: req.SFTP.Path, Auth: req.SFTP.Auth, KeyPath: req.SFTP.KeyPath,
			},
		})
		d.ExtraOptions = raw
	}
	if req.Enabled != nil {
		d.Enabled = *req.Enabled
	}
	if req.ClearCreds {
		if d.CredentialsRef != nil {
			h.deleteCreds(c.Request.Context(), d.ID)
		}
		d.CredentialsRef = nil
	}
	credsEnv := req.CredentialsEnv
	if d.Kind == models.BackupDestinationKindSFTP && req.SFTP != nil &&
		req.SFTP.Auth == models.SFTPAuthPassword && req.SFTPPassword != "" {
		if credsEnv == nil {
			credsEnv = map[string]string{}
		}
		credsEnv["SSHPASS"] = req.SFTPPassword
	}
	if len(credsEnv) > 0 {
		path, err := h.writeCreds(c.Request.Context(), d.ID, credsEnv)
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
		h.deleteCreds(c.Request.Context(), d.ID)
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
	if h.agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "error": "agent_unavailable"})
		return
	}
	var credsRef string
	if d.CredentialsRef != nil {
		credsRef = *d.CredentialsRef
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60_000_000_000)
	defer cancel()
	payload := map[string]any{
		"url":             d.URL,
		"credentials_ref": credsRef,
		"extra_options":   backupwrapperhelpers.ResticOptionsFor(d),
	}
	if d.Kind == models.BackupDestinationKindSFTP {
		if s := d.ExtraOptionsTyped().SFTP; s != nil {
			payload["sftp"] = map[string]any{
				"host":     s.Host,
				"user":     s.User,
				"port":     s.Port,
				"path":     s.Path,
				"auth":     s.Auth,
				"key_path": s.KeyPath,
			}
		}
	}
	raw, err := h.agent.Call(ctx, "backup.dest.test", payload)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"status": "error", "error": "agent_call", "detail": err.Error()})
		return
	}
	var resp struct {
		Status        string `json:"status"`
		StdoutPreview string `json:"stdout_preview,omitempty"`
		Stderr        string `json:"stderr,omitempty"`
		Detail        string `json:"detail,omitempty"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "agent_reply_parse"})
		return
	}
	if resp.Status != "ok" {
		c.JSON(http.StatusBadGateway, gin.H{
			"status": "error",
			"error":  "restic_test_failed",
			"detail": resp.Detail,
			"stderr": resp.Stderr,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":         "ok",
		"stdout_preview": resp.StdoutPreview,
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

