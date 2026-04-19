// Package api - /api/v1/files HTTP handlers (M11 Wave C).
// Handlers are thin: auth → dispatch to panel-agent files.* command → JSON.
// All filesystem safety is enforced inside panel-agent via the filesafe package.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// Agent param struct types. JSON tags must match panel-agent/internal/commands/files_*.go
// exactly — see files_wire_test.go for the drift-guard test.
type filesListAgentParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Path     string `json:"path"`
}

type filesReadAgentParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Path     string `json:"path"`
	Limit    int64  `json:"limit,omitempty"`
}

type filesWriteAgentParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Path     string `json:"path"`
	Content  string `json:"content"`
	Mode     string `json:"mode,omitempty"`
}

type filesDeleteAgentParams struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Path      string `json:"path"`
	Recursive bool   `json:"recursive,omitempty"`
}

type filesMkdirAgentParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Path     string `json:"path"`
	Mode     string `json:"mode,omitempty"`
}

type filesRenameAgentParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	OldPath  string `json:"old_path"`
	NewPath  string `json:"new_path"`
}

type filesStatAgentParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Path     string `json:"path"`
}

// FilesHandlerConfig bundles dependencies for /api/v1/files.
type FilesHandlerConfig struct {
	Users   repository.UserRepository
	Domains repository.DomainRepository
	Agent   agent.AgentInterface
	Log     *slog.Logger
}

const (
	maxUploadBytes  int64 = 100 * 1024 * 1024
	maxPreviewBytes int64 = 1 * 1024 * 1024
)

// RegisterFilesRoutes mounts /files under the given group (expected /api/v1).
// All routes require an authenticated caller.
func RegisterFilesRoutes(g *gin.RouterGroup, cfg FilesHandlerConfig) {
	h := &filesHandler{cfg: cfg}
	grp := g.Group("/files")
	grp.GET("", h.list)
	grp.DELETE("", h.delete)
	grp.GET("/home", h.home)
	grp.GET("/tree", h.tree)
	grp.GET("/download", h.download)
	grp.GET("/preview", h.preview)
	grp.POST("/upload", filesUploadSizeLimit(maxUploadBytes), h.upload)
	grp.POST("/mkdir", h.mkdir)
	grp.POST("/rename", h.rename)
}

type filesHandler struct{ cfg FilesHandlerConfig }

type mkdirRequest struct {
	Path string `json:"path" binding:"required"`
}

type renameRequest struct {
	Path    string `json:"path" binding:"required"`
	NewName string `json:"new_name" binding:"required"`
}

type filesListEntry struct {
	Name      string `json:"name"`
	IsDir     bool   `json:"is_dir"`
	Size      int64  `json:"size"`
	Mode      string `json:"mode"`
	ModTime   string `json:"mod_time"`
	IsSymlink bool   `json:"is_symlink"`
}

type filesListAgentResult struct {
	Path    string           `json:"path"`
	Entries []filesListEntry `json:"entries"`
}

type filesReadAgentResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Size    int64  `json:"size"`
}

// ---- helpers ----

func (h *filesHandler) requireClaimsAndUsername(c *gin.Context) (userID, username string, ok bool) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return "", "", false
	}
	name, err := h.linuxUsername(c.Request.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) || err.Error() == "user has no linux account" {
			c.JSON(http.StatusForbidden, gin.H{"error": "no_linux_account"})
			return "", "", false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return "", "", false
	}
	return claims.UserID, name, true
}

func (h *filesHandler) linuxUsername(ctx context.Context, userID string) (string, error) {
	u, err := h.cfg.Users.FindByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if u == nil || u.Username == nil || *u.Username == "" {
		return "", errors.New("user has no linux account")
	}
	return *u.Username, nil
}

func requirePath(c *gin.Context) (string, bool) {
	p := c.Query("path")
	if p == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path_required"})
		return "", false
	}
	return p, true
}

// agentErrorStatus maps panel-agent error text to HTTP status. The agent
// returns structured errors via agentwire; match on well-known substrings
// so we return 400/403/404 where appropriate rather than blanket 500.
func agentErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not_in_scope"),
		strings.Contains(msg, "symlink_escape"),
		strings.Contains(msg, "path_traversal"):
		return http.StatusForbidden
	case strings.Contains(msg, "contains_null"),
		strings.Contains(msg, "bad_characters"),
		strings.Contains(msg, "not_absolute"),
		strings.Contains(msg, "too_long"),
		strings.Contains(msg, "invalid"):
		return http.StatusBadRequest
	case strings.Contains(msg, "no such file"),
		strings.Contains(msg, "not_found"):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func respondAgentError(c *gin.Context, err error) {
	c.JSON(agentErrorStatus(err), gin.H{"error": "agent_error", "detail": err.Error()})
}

// ---- handlers ----

// home returns the authenticated user's starting directory. The UI calls
// this on mount to avoid needing client-side knowledge of unix username
// mapping.
func (h *filesHandler) home(c *gin.Context) {
	_, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": "/home/" + username})
}

func (h *filesHandler) list(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	p, ok := requirePath(c)
	if !ok {
		return
	}
	raw, err := h.cfg.Agent.Call(c.Request.Context(), "files.list", filesListAgentParams{
		UserID: userID, Username: username, Path: p,
	})
	if err != nil {
		respondAgentError(c, err)
		return
	}
	var result filesListAgentResult
	if err := json.Unmarshal(raw, &result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "detail": "bad agent response"})
		return
	}
	c.JSON(http.StatusOK, result)
}

// tree: same as list but filtered to non-symlink directories (lazy expansion).
func (h *filesHandler) tree(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	p, ok := requirePath(c)
	if !ok {
		return
	}
	raw, err := h.cfg.Agent.Call(c.Request.Context(), "files.list", filesListAgentParams{
		UserID: userID, Username: username, Path: p,
	})
	if err != nil {
		respondAgentError(c, err)
		return
	}
	var result filesListAgentResult
	if err := json.Unmarshal(raw, &result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "detail": "bad agent response"})
		return
	}
	dirs := make([]filesListEntry, 0, len(result.Entries))
	for _, e := range result.Entries {
		if e.IsDir && !e.IsSymlink {
			dirs = append(dirs, e)
		}
	}
	c.JSON(http.StatusOK, filesListAgentResult{Path: result.Path, Entries: dirs})
}

// download streams file bytes back as an attachment. MVP limitation: the
// agent returns content as a JSON string, so invalid-UTF-8 binary bytes are
// lossy — acceptable for text files; binary is Phase 2.
func (h *filesHandler) download(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	p, ok := requirePath(c)
	if !ok {
		return
	}
	raw, err := h.cfg.Agent.Call(c.Request.Context(), "files.read", filesReadAgentParams{
		UserID: userID, Username: username, Path: p, Limit: maxUploadBytes,
	})
	if err != nil {
		respondAgentError(c, err)
		return
	}
	var result filesReadAgentResult
	if err := json.Unmarshal(raw, &result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "detail": "bad agent response"})
		return
	}
	filename := filepath.Base(p)
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Header("X-Content-Type-Options", "nosniff")
	c.String(http.StatusOK, result.Content)
}

// preview returns text content capped at 1 MiB as a JSON envelope.
func (h *filesHandler) preview(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	p, ok := requirePath(c)
	if !ok {
		return
	}
	raw, err := h.cfg.Agent.Call(c.Request.Context(), "files.read", filesReadAgentParams{
		UserID: userID, Username: username, Path: p, Limit: maxPreviewBytes,
	})
	if err != nil {
		respondAgentError(c, err)
		return
	}
	var result filesReadAgentResult
	if err := json.Unmarshal(raw, &result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "detail": "bad agent response"})
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", "inline")
	c.JSON(http.StatusOK, gin.H{
		"path":    result.Path,
		"size":    result.Size,
		"content": result.Content,
	})
}

// upload accepts a single multipart file under the "file" field; directory
// is taken from ?path=. Cap enforced at middleware (filesUploadSizeLimit).
func (h *filesHandler) upload(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	dirPath, ok := requirePath(c)
	if !ok {
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "file_required"})
			return
		}
		if strings.Contains(err.Error(), "request body too large") {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file_too_large"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	if fileHeader.Size > maxUploadBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file_too_large"})
		return
	}
	// Reject filenames with path separators; upload target is a single-segment name.
	if strings.ContainsAny(fileHeader.Filename, "/\\") || fileHeader.Filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_filename"})
		return
	}
	src, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}
	defer src.Close()
	buf, err := io.ReadAll(io.LimitReader(src, maxUploadBytes+1))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}
	if int64(len(buf)) > maxUploadBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file_too_large"})
		return
	}
	targetPath := filepath.Join(dirPath, fileHeader.Filename)

	raw, err := h.cfg.Agent.Call(c.Request.Context(), "files.write", filesWriteAgentParams{
		UserID: userID, Username: username, Path: targetPath, Content: string(buf),
	})
	if err != nil {
		respondAgentError(c, err)
		return
	}
	var result struct {
		Path         string `json:"path"`
		BytesWritten int64  `json:"bytes_written"`
	}
	_ = json.Unmarshal(raw, &result)
	c.JSON(http.StatusOK, gin.H{"path": result.Path, "bytes_written": result.BytesWritten})
}

func (h *filesHandler) mkdir(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	var req mkdirRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	_, err := h.cfg.Agent.Call(c.Request.Context(), "files.mkdir", filesMkdirAgentParams{
		UserID: userID, Username: username, Path: req.Path,
	})
	if err != nil {
		respondAgentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": req.Path})
}

func (h *filesHandler) rename(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	var req renameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	// new_name is a single path segment — no separators, no .. escape.
	if strings.ContainsAny(req.NewName, "/\\") || req.NewName == "." || req.NewName == ".." {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_new_name"})
		return
	}
	newPath := filepath.Join(filepath.Dir(req.Path), req.NewName)
	_, err := h.cfg.Agent.Call(c.Request.Context(), "files.rename", filesRenameAgentParams{
		UserID: userID, Username: username, OldPath: req.Path, NewPath: newPath,
	})
	if err != nil {
		respondAgentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"old_path": req.Path, "new_path": newPath})
}

func (h *filesHandler) delete(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	p, ok := requirePath(c)
	if !ok {
		return
	}
	recursive := c.Query("recursive") == "true"
	_, err := h.cfg.Agent.Call(c.Request.Context(), "files.delete", filesDeleteAgentParams{
		UserID: userID, Username: username, Path: p, Recursive: recursive,
	})
	if err != nil {
		respondAgentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": p})
}
