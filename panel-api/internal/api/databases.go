package api

import (
	"strings"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DatabaseHandlerConfig plugs the database handlers into the router.
type DatabaseHandlerConfig struct {
	Databases      repository.DatabaseRepository
	DatabaseUsers  repository.DatabaseUserRepository
	Users          repository.UserRepository
	Packages       repository.PackageRepository
	Agent          agent.AgentInterface
}

const (
	defaultDatabasesPageSize = 20
	maxDatabasesPageSize     = 200
)

// RegisterDatabaseRoutes mounts /databases* under g.
// - GET /databases (admin: all; user: scoped to self)
// - GET /databases/:id (admin: all; user: scoped to self)
// - POST /databases (admin: all; user: own only)
// - DELETE /databases/:id (admin: all; user: own only)
// - GET /databases/:id/backup (admin: all; user: scoped to self)
// - POST /databases/:id/restore (admin: all; user: scoped to self)
func RegisterDatabaseRoutes(g *gin.RouterGroup, cfg DatabaseHandlerConfig) {
	h := &databaseHandler{cfg: cfg}

	databases := g.Group("/databases")
	databases.GET("", h.list)
	databases.GET("/:id", h.get)
	databases.POST("", h.create)
	databases.DELETE("/:id", h.delete)
	databases.GET("/:id/backup", h.backup)
	databases.POST("/:id/restore", h.restore)
}

type databaseHandler struct{ cfg DatabaseHandlerConfig }

// databaseListRow is returned by the list endpoint; it embeds the database model
// and adds a computed size_bytes field fetched from the agent.
type databaseListRow struct {
	models.Database
	SizeBytes int64 `json:"size_bytes"`
}

// ---- helpers ----

// openFile opens a file for reading. Returns an error if the file does not exist or cannot be read.
func openFile(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	return f, nil
}

// copyFile copies from src to dst using io.Copy. This streams the data without buffering
// the entire file in memory.
func copyFile(dst io.Writer, src io.Reader) (int64, error) {
	n, err := io.Copy(dst, src)
	if err != nil {
		return n, fmt.Errorf("failed to copy file: %w", err)
	}
	return n, nil
}

// deleteFile removes a file at the given path. Errors are logged but not returned.
func deleteFile(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return nil
}

// createDir creates a directory with mode 0700 if it does not exist.
func createDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	return nil
}

// writeToFile writes the contents of a multipart file to disk at the given path with mode 0600.
// It ensures the parent directory exists.
func writeToFile(path string, src io.Reader, size int64) error {
	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Create the file with mode 0600
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	// Copy the multipart file to disk
	if _, err := io.Copy(f, src); err != nil {
		// Clean up the partial file
		_ = os.Remove(path)
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// ---- handlers ----

func (h *databaseHandler) list(c *gin.Context) {
	page, pageSize, opts := parseListOptions(c, defaultDatabasesPageSize, maxDatabasesPageSize)

	// Get current user claims
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var dbs []models.Database
	var total int64
	var err error

	// Admins see all databases; users see only their own
	if claims.IsAdmin {
		dbs, total, err = h.cfg.Databases.List(c.Request.Context(), opts)
	} else {
		dbs, total, err = h.cfg.Databases.ListByUserID(c.Request.Context(), claims.UserID, opts)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if dbs == nil {
		dbs = []models.Database{}
	}

	// For non-admins, enforce the panel-wide naming convention: a user
	// can only see databases whose names start with their linux-username
	// prefix. Belt-and-suspenders on top of the user_id FK filter — if
	// a row ever lands with the wrong user_id (e.g. via direct DB edit
	// or a legacy WP install that skipped prefixing), it stays hidden.
	if !claims.IsAdmin {
		if u, uErr := h.cfg.Users.FindByID(c.Request.Context(), claims.UserID); uErr == nil && u != nil && u.Username != nil && *u.Username != "" {
			prefix := *u.Username + "_"
			filtered := dbs[:0]
			for _, d := range dbs {
				if strings.HasPrefix(d.Name, prefix) {
					filtered = append(filtered, d)
				}
			}
			if len(filtered) != len(dbs) {
				total -= int64(len(dbs) - len(filtered))
			}
			dbs = filtered
		}
	}

	// Fetch size_bytes for each database. Use a 30-second timeout for all size calls.
	// If any call fails, degrade to size_bytes=0 for that row and log at INFO.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	rows := make([]databaseListRow, len(dbs))
	for i, db := range dbs {
		rows[i] = databaseListRow{Database: db, SizeBytes: 0}

		// Fetch size from agent
		result, err := h.cfg.Agent.Call(ctx, "db.size", map[string]string{"db_name": db.Name})
		if err != nil {
			// Log at INFO and continue with size_bytes=0
			slog.Info("failed to fetch database size",
				"db_name", db.Name,
				"error", err.Error())
			continue
		}

		// Parse the size_bytes from the response
		var resp struct {
			SizeBytes int64 `json:"size_bytes"`
		}
		if err := json.Unmarshal(result, &resp); err != nil {
			slog.Info("failed to parse database size response",
				"db_name", db.Name,
				"error", err.Error())
			continue
		}

		rows[i].SizeBytes = resp.SizeBytes
	}

	c.JSON(http.StatusOK, gin.H{
		"data":      rows,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *databaseHandler) get(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	db, err := h.cfg.Databases.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check authorization: admins can access any database; users can only access their own
	if !claims.IsAdmin && db.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	c.JSON(http.StatusOK, db)
}

type createDatabaseRequest struct {
	Name string `json:"name" binding:"required"`
}

func (h *databaseHandler) create(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req createDatabaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	// Validate database name: ^[a-z][a-z0-9_]{0,30}$ (max 30 leaves room for username_ prefix)
	if !databaseNameValid(req.Name) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_database_name",
			"detail": "database name must match regex ^[a-z][a-z0-9_]{0,30}$",
		})
		return
	}

	ctx := c.Request.Context()
	targetUserID := claims.UserID

	// Load user and check for package/quota
	user, err := h.cfg.Users.FindByID(ctx, targetUserID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Non-admin users must have a username for the database prefix
	if !claims.IsAdmin && (user.Username == nil || *user.Username == "") {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check quota
	max := int64(0)
	if user.PackageID != nil && *user.PackageID != "" {
		pkg, err := h.cfg.Packages.FindByID(ctx, *user.PackageID)
		if err == nil && pkg.MaxDatabases > 0 {
			max = int64(pkg.MaxDatabases)
			count, err := h.cfg.Databases.CountByUserID(ctx, targetUserID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
				return
			}
			if count >= max {
				c.JSON(http.StatusConflict, gin.H{
					"error":    "quota_exceeded",
					"resource": "databases",
					"limit":    max,
				})
				return
			}
		}
	}

	// Compute final name with username prefix
	var finalName string
	if claims.IsAdmin {
		finalName = req.Name
	} else {
		finalName = *user.Username + "_" + req.Name
	}

	// Check for collision on the FINAL (prefixed) name — that's what
	// MariaDB sees and what we store in the row, so uniqueness is meaningful.
	exists, err := h.cfg.Databases.ExistsByUserAndName(ctx, targetUserID, finalName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if exists {
		c.JSON(http.StatusConflict, gin.H{"error": "database_name_exists"})
		return
	}

	// Call agent to create the database
	if h.cfg.Agent == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err = h.cfg.Agent.Call(agentCtx, "db.create", map[string]any{
		"db_name":   finalName,
		"charset":   "utf8mb4",
		"collation": "utf8mb4_unicode_ci",
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	// Persist to database
	now := time.Now().UTC()
	d := &models.Database{
		ID:        ids.NewULID(),
		UserID:    targetUserID,
		Name:      finalName,
		Engine:    "mariadb",
		Charset:   "utf8mb4",
		Collation: "utf8mb4_unicode_ci",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := h.cfg.Databases.Create(ctx, d); err != nil {
		if isConflict(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "database_name_exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.JSON(http.StatusCreated, d)
}

func (h *databaseHandler) delete(c *gin.Context) {
	ctx := c.Request.Context()

	// Load the database first
	d, err := h.cfg.Databases.FindByID(ctx, c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Check authorization: admins can delete any; users only their own
	if !claims.IsAdmin && d.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Call agent to drop the database
	if h.cfg.Agent == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// d.Name is the full MariaDB-side name (prefix already baked in at
	// create time) so we pass it to the agent verbatim.
	_, err = h.cfg.Agent.Call(agentCtx, "db.drop", map[string]any{
		"db_name": d.Name,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	// Delete from database
	if err := h.cfg.Databases.Delete(ctx, d.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *databaseHandler) backup(c *gin.Context) {
	ctx := c.Request.Context()

	// Load the database first
	d, err := h.cfg.Databases.FindByID(ctx, c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Check authorization: admins can backup any; users only their own
	if !claims.IsAdmin && d.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Call agent to create the backup
	if h.cfg.Agent == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	agentCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	result, err := h.cfg.Agent.Call(agentCtx, "db.backup", map[string]any{
		"db_name": d.Name,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	// Parse the backup response
	var resp struct {
		Path      string `json:"path"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Open the backup file
	f, err := openFile(resp.Path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	defer f.Close()

	// Set response headers for download
	c.Header("Content-Disposition", "attachment; filename=\""+d.Name+"-"+time.Now().Format("20060102-150405")+".sql\"")
	c.Header("Content-Type", "application/sql")
	c.Header("Content-Length", fmt.Sprintf("%d", resp.SizeBytes))

	// Stream the file to the client
	if _, err := copyFile(c.Writer, f); err != nil {
		// Log the error but don't send response (headers already sent)
		slog.Error("failed to stream backup file", "error", err)
	}

	// Delete the temp file after streaming
	_ = deleteFile(resp.Path)
}

func (h *databaseHandler) restore(c *gin.Context) {
	ctx := c.Request.Context()

	// Load the database first
	d, err := h.cfg.Databases.FindByID(ctx, c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Check authorization: admins can restore any; users only their own
	if !claims.IsAdmin && d.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Parse multipart form (max 500 MB)
	const maxUploadSize = 500 * 1024 * 1024 // 500 MB
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadSize)
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil { // 32 MB in-memory
		if err.Error() == "http: request body too large" {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file_too_large", "max_size": "500MB"})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		}
		return
	}

	header, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_file"})
		return
	}

	file, err := header.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	defer file.Close()

	// Generate restore file path
	ul := ids.NewULID()
	restorePath := fmt.Sprintf("/var/lib/jabali/restore/%s.sql", ul)

	// Create restore directory if needed
	if err := createDir("/var/lib/jabali/restore"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Write uploaded file to /var/lib/jabali/restore/
	if err := writeToFile(restorePath, file, header.Size); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Call agent to restore the database
	if h.cfg.Agent == nil {
		_ = deleteFile(restorePath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	agentCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	_, err = h.cfg.Agent.Call(agentCtx, "db.restore", map[string]any{
		"db_name": d.Name,
		"path":    restorePath,
	})
	if err != nil {
		// Agent cleanup the file on failure, but delete it here too just in case
		_ = deleteFile(restorePath)
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

// databaseNameValid validates a database name against the required pattern
func databaseNameValid(name string) bool {
	if len(name) == 0 || len(name) > 30 {
		return false
	}
	// Must start with lowercase letter, followed by lowercase letters, digits, or underscores
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return false
		}
	}
	return true
}
