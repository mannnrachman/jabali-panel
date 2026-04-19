// Package api - /api/v1/files HTTP handlers (M11 Wave C).
// Handlers are thin: auth → dispatch to panel-agent files.* command → JSON.
// All filesystem safety is enforced inside panel-agent via the filesafe package.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
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

type filesMoveAgentParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	OldPath  string `json:"old_path"`
	NewPath  string `json:"new_path"`
}

type filesChmodAgentParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Path     string `json:"path"`
	Mode     string `json:"mode"`
}

type filesArchiveAgentParams struct {
	UserID   string   `json:"user_id"`
	Username string   `json:"username"`
	Paths    []string `json:"paths"`
}

type filesArchiveAgentResult struct {
	ArchivePath string `json:"archive_path"`
	Size        int64  `json:"size"`
}

type filesCopyAgentParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	SrcPath  string `json:"src_path"`
	DstPath  string `json:"dst_path"`
}

type filesIngestAgentParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	TmpPath  string `json:"tmp_path"`
	DestPath string `json:"dest_path"`
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
	grp.POST("/move", h.move)
	grp.POST("/chmod", h.chmod)
	grp.POST("/archive", h.archive)
	grp.POST("/copy", h.copy)
	grp.POST("/write", h.write)
	grp.POST("/upload-chunk", h.uploadChunk)
}

type filesHandler struct{ cfg FilesHandlerConfig }

type mkdirRequest struct {
	Path string `json:"path" binding:"required"`
}

type renameRequest struct {
	Path    string `json:"path" binding:"required"`
	NewName string `json:"new_name" binding:"required"`
}

// moveRequest is used by the drag-and-drop flow: dragging a row onto a
// folder sends the original path and the new *parent directory*; the
// handler combines them to form the destination path, preserving the
// basename. This mirrors desktop drag-to-move semantics and avoids
// letting the client smuggle an arbitrary destination name.
type moveRequest struct {
	Path    string `json:"path"     binding:"required"`
	DestDir string `json:"dest_dir" binding:"required"`
}

// chmodRequest powers the bulk-permissions modal. Mode is an octal
// string like "0644"; the agent validates the format and masks it to
// the low 12 bits, so we don't replicate the parse here — sending a
// bogus mode just gets a 400 back from the agent.
type chmodRequest struct {
	Path string `json:"path" binding:"required"`
	Mode string `json:"mode" binding:"required"`
}

// archiveRequest powers the "Download .tar.gz of N selected items"
// flow. Paths are absolute, scoped inside the user's homedir — the
// agent re-validates every entry before building anything.
type archiveRequest struct {
	Paths []string `json:"paths" binding:"required"`
}

// copyRequest powers the Copy/Paste flow. `dest_dir` is the parent
// directory to land into; the basename is preserved from src so the
// client can't name a destination collision it wouldn't have picked
// interactively.
type copyRequest struct {
	Path    string `json:"path"     binding:"required"`
	DestDir string `json:"dest_dir" binding:"required"`
}

// writeRequest powers the Monaco editor's Save action. Content is
// UTF-8; binary-safe writes (base64) are deferred to Phase-3.
type writeRequest struct {
	Path    string `json:"path"    binding:"required"`
	Content string `json:"content"`
}

type filesListEntry struct {
	Name       string `json:"name"`
	IsDir      bool   `json:"is_dir"`
	Size       int64  `json:"size"`
	Mode       string `json:"mode"`
	ModTime    string `json:"mod_time"`
	IsSymlink  bool   `json:"is_symlink"`
	HasSubdirs bool   `json:"has_subdirs,omitempty"`
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

// move handles POST /files/move: relocate a file or directory into a
// different parent directory. Semantically distinct from rename, which
// is same-parent only. Used by the drag-and-drop flow.
func (h *filesHandler) move(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	var req moveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	// Dest must look like an absolute path (validated inside the agent
	// against the user's scope), never bare "..". Client should not be
	// able to coerce us into moving into the docroot's parent by sending
	// just "..".
	if strings.Contains(req.DestDir, "..") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_dest_dir"})
		return
	}
	newPath := filepath.Join(req.DestDir, filepath.Base(req.Path))
	_, err := h.cfg.Agent.Call(c.Request.Context(), "files.move", filesMoveAgentParams{
		UserID: userID, Username: username, OldPath: req.Path, NewPath: newPath,
	})
	if err != nil {
		respondAgentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"old_path": req.Path, "new_path": newPath})
}

// chmod handles POST /files/chmod: set permission bits on a single file
// or directory. Bulk chmod from the UI loops this endpoint per entry —
// keeps the backend contract small (one path per call) and lets the
// frontend surface per-item failures without an all-or-nothing atomic
// guarantee we can't actually deliver on a real filesystem.
func (h *filesHandler) chmod(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	var req chmodRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	_, err := h.cfg.Agent.Call(c.Request.Context(), "files.chmod", filesChmodAgentParams{
		UserID: userID, Username: username, Path: req.Path, Mode: req.Mode,
	})
	if err != nil {
		respondAgentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": req.Path, "mode": req.Mode})
}

// archive handles POST /files/archive: the agent builds a tar.gz of
// every requested path into a tmp file owned by the panel process, we
// stream that file back to the client with the right headers, then
// unlink. One HTTP request per download — no cleanup token dance.
func (h *filesHandler) archive(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	var req archiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	if len(req.Paths) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "paths_required"})
		return
	}
	raw, err := h.cfg.Agent.Call(c.Request.Context(), "files.archive", filesArchiveAgentParams{
		UserID: userID, Username: username, Paths: req.Paths,
	})
	if err != nil {
		respondAgentError(c, err)
		return
	}
	var result filesArchiveAgentResult
	if err := json.Unmarshal(raw, &result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "detail": "bad agent response"})
		return
	}
	// Always remove the scratch file after we're done streaming —
	// success, client disconnect, or stat failure. A leftover /tmp
	// file would accumulate per-download otherwise.
	defer func() { _ = os.Remove(result.ArchivePath) }()

	f, err := os.Open(result.ArchivePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "detail": err.Error()})
		return
	}
	defer f.Close()

	c.Header("Content-Type", "application/gzip")
	c.Header("Content-Disposition", `attachment; filename="archive.tar.gz"`)
	c.Header("Content-Length", fmt.Sprintf("%d", result.Size))
	if _, err := io.Copy(c.Writer, f); err != nil {
		// Response already started — nothing useful to do, just log
		// via the request logger.
		if h.cfg.Log != nil {
			h.cfg.Log.Warn("archive stream failed", "err", err, "path", result.ArchivePath)
		}
	}
}

// copy handles POST /files/copy: recursively copies a scoped path
// into a different parent directory. Distinct from move (which
// relinks) and rename (same-parent-only).
func (h *filesHandler) copy(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	var req copyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	if strings.Contains(req.DestDir, "..") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_dest_dir"})
		return
	}
	dst := filepath.Join(req.DestDir, filepath.Base(req.Path))
	_, err := h.cfg.Agent.Call(c.Request.Context(), "files.copy", filesCopyAgentParams{
		UserID: userID, Username: username, SrcPath: req.Path, DstPath: dst,
	})
	if err != nil {
		respondAgentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"src_path": req.Path, "dst_path": dst})
}

// write handles POST /files/write: overwrites / creates a file with
// the given UTF-8 content. Used by the Monaco editor's Save action.
func (h *filesHandler) write(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	var req writeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	_, err := h.cfg.Agent.Call(c.Request.Context(), "files.write", filesWriteAgentParams{
		UserID: userID, Username: username, Path: req.Path, Content: req.Content,
	})
	if err != nil {
		respondAgentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": req.Path, "size": len(req.Content)})
}

// uploadChunk handles POST /files/upload-chunk: streams a binary chunk
// to /tmp/jabali-upload-<id>, appending at `offset`. When `final=1` is
// set on the last chunk, the accumulated /tmp file is moved into the
// user's scope at `path/name` via the agent's files.ingest command.
//
// No auth other than the usual user check — upload_ids are random UUIDs
// kept only in the client's memory, so there's no cross-user collision
// surface unless two separate clients both guess the same 128-bit id.
func (h *filesHandler) uploadChunk(c *gin.Context) {
	userID, username, ok := h.requireClaimsAndUsername(c)
	if !ok {
		return
	}
	uploadID := c.Query("upload_id")
	offsetStr := c.Query("offset")
	destDir := c.Query("path")
	filename := c.Query("name")
	isFinal := c.Query("final") == "1"
	if uploadID == "" || offsetStr == "" || destDir == "" || filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_upload_params"})
		return
	}
	// Keep uploadID safe as a filesystem basename — no path separators,
	// no ".." — since we string-interpolate it into /tmp/<id>.
	if strings.ContainsAny(uploadID, "/\\.") || uploadID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_upload_id"})
		return
	}
	if strings.ContainsAny(filename, "/\\") || filename == "." || filename == ".." {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_filename"})
		return
	}
	var offset int64
	if _, err := fmt.Sscanf(offsetStr, "%d", &offset); err != nil || offset < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_offset"})
		return
	}
	tmpPath := fmt.Sprintf("/tmp/jabali-upload-%s", uploadID)

	// Open for read-write-create; APPEND is wrong here because the
	// client may re-send a chunk after a network blip — we want to
	// position by offset, not seek-to-end.
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "detail": err.Error()})
		return
	}
	if _, err := f.Seek(offset, 0); err != nil {
		f.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "detail": err.Error()})
		return
	}
	// Cap at 1 GB total — huge uploads are fine but runaway sizes
	// would fill /tmp. Check via stat AFTER the write to keep the
	// per-chunk hot path simple.
	const maxUploadSize = int64(1024 * 1024 * 1024)
	written, copyErr := io.Copy(f, io.LimitReader(c.Request.Body, maxUploadSize-offset+1))
	if cerr := f.Close(); copyErr == nil {
		copyErr = cerr
	}
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "detail": copyErr.Error()})
		return
	}
	if offset+written > maxUploadSize {
		_ = os.Remove(tmpPath)
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file_too_large"})
		return
	}

	if !isFinal {
		c.JSON(http.StatusOK, gin.H{"upload_id": uploadID, "written": written})
		return
	}

	// Final chunk: hand off to the agent to ingest the /tmp file into
	// the user's homedir scope at the requested path/name.
	destPath := filepath.Join(destDir, filename)
	_, err = h.cfg.Agent.Call(c.Request.Context(), "files.ingest", filesIngestAgentParams{
		UserID: userID, Username: username, TmpPath: tmpPath, DestPath: destPath,
	})
	if err != nil {
		_ = os.Remove(tmpPath)
		respondAgentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": destPath})
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
