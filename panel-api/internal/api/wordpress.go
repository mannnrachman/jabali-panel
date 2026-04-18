package api

import (
	"context"
	"log/slog"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// WordPressHandlerConfig bundles repositories and services for WordPress handlers.
type WordPressHandlerConfig struct {
	WordPressInstalls   repository.WordPressInstallRepository
	Databases           repository.DatabaseRepository
	DatabaseUsers       repository.DatabaseUserRepository
	DatabaseGrants      repository.DatabaseUserGrantRepository
	Domains             repository.DomainRepository
	Users               repository.UserRepository
	Packages            repository.PackageRepository
	Agent               agent.AgentInterface
}

// RegisterWordPressRoutes registers WordPress routes.
func RegisterWordPressRoutes(g *gin.RouterGroup, cfg WordPressHandlerConfig) {
	h := &wordPressHandler{cfg: cfg}

	installs := g.Group("/wordpress-installs")
	installs.POST("", h.create)
	installs.GET("", h.list)
	installs.GET("/:id", h.get)
	installs.DELETE("/:id", h.delete)
	installs.POST("/:id/clone", h.clone)
	installs.POST("/:id/health", h.health)
}

type wordPressHandler struct{ cfg WordPressHandlerConfig }

// ---- Request/Response types ----

type createWordPressRequest struct {
	DomainID       string `json:"domain_id" binding:"required"`
	SiteTitle      string `json:"site_title" binding:"required"`
	AdminUsername  string `json:"admin_username" binding:"required"`
	AdminEmail     string `json:"admin_email" binding:"required"`
	AdminPassword  string `json:"admin_password"`
	Locale         string `json:"locale"`
}

type cloneWordPressRequest struct {
	DestDomainID string `json:"dest_domain_id" binding:"required"`
}

type createWordPressResponse struct {
	ID            string    `json:"id"`
	DomainID      string    `json:"domain_id"`
	DBID          string    `json:"db_id"`
	AdminUsername string    `json:"admin_username"`
	AdminPassword string    `json:"admin_password"`
	AdminEmail    string    `json:"admin_email"`
	Status        string    `json:"status"`
	Version       *string   `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type wordPressListResponse struct {
	ID            string    `json:"id"`
	DomainID      string    `json:"domain_id"`
	DomainName    string    `json:"domain_name"`
	DBID          string    `json:"db_id"`
	AdminUsername string    `json:"admin_username"`
	AdminEmail    string    `json:"admin_email"`
	Locale        string    `json:"locale"`
	Status        string    `json:"status"`
	Version       *string   `json:"version"`
	LastError     string    `json:"last_error"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type healthResponse struct {
	WPInstalled bool   `json:"wp_installed"`
	WPVersion   string `json:"wp_version"`
	HTTPStatus  int    `json:"http_status"`
}

// ---- Handlers ----

func (h *wordPressHandler) create(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req createWordPressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	// Validate email
	if !isValidEmail(req.AdminEmail) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_email"})
		return
	}

	ctx := c.Request.Context()
	targetUserID := claims.UserID

	// Verify domain ownership (404 if not owner, 403 if cross-user)
	domain, err := h.cfg.Domains.FindByID(ctx, req.DomainID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		slog.ErrorContext(ctx, "wordpress create: domain lookup failed", "err", err, "domain_id", req.DomainID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if domain.UserID != targetUserID {
		if claims.IsAdmin {
			// Admin can operate on any domain, but reject if different user owns it
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
		return
	}

	// Check for duplicate install on same domain
	existing, err := h.cfg.WordPressInstalls.FindByDomainID(ctx, req.DomainID)
	if err == nil && existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "install_exists"})
		return
	}
	if err != nil && !isNotFound(err) {
		slog.ErrorContext(ctx, "wordpress create: existing install lookup failed", "err", err, "domain_id", req.DomainID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Generate admin password if not provided
	adminPassword := req.AdminPassword
	if adminPassword == "" {
		adminPassword = ids.NewULID()
	}

	now := time.Now().UTC()

	// Provision database (database name = wp_<6-char ULID prefix>)
	dbID := ids.NewULID()
	dbName := "wp_" + dbID[:6]
	database := &models.Database{
		ID:        dbID,
		UserID:    targetUserID,
		Name:      dbName,
		Engine:    "mariadb",
		Charset:   "utf8mb4",
		Collation: "utf8mb4_unicode_ci",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := h.cfg.Databases.Create(ctx, database); err != nil {
		slog.ErrorContext(ctx, "wordpress create: database row create failed", "err", err, "db_id", dbID, "db_name", dbName)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Provision database user
	dbUserID := ids.NewULID()
	dbUsername := "wp_" + dbUserID[:6]
	hash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
	if err != nil {
		slog.ErrorContext(ctx, "wordpress create: bcrypt failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	databaseUser := &models.DatabaseUser{
		ID:           dbUserID,
		UserID:       targetUserID,
		Username:     dbUsername,
		PasswordHash: string(hash),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.cfg.DatabaseUsers.Create(ctx, databaseUser); err != nil {
		// Rollback database
		h.cfg.Databases.Delete(ctx, dbID)
		slog.ErrorContext(ctx, "wordpress create: database user create failed", "err", err, "db_user_id", dbUserID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Provision grant
	grantID := ids.NewULID()
	grant := &models.DatabaseUserGrant{
		ID:               grantID,
		DatabaseUserID:   dbUserID,
		DatabaseID:       dbID,
		GrantLevel:       "all",
		Privileges:       "ALL",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := h.cfg.DatabaseGrants.Create(ctx, grant); err != nil {
		// Rollback database and user
		h.cfg.DatabaseUsers.Delete(ctx, dbUserID)
		h.cfg.Databases.Delete(ctx, dbID)
		slog.ErrorContext(ctx, "wordpress create: grant create failed", "err", err, "grant_id", grantID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Create WordPress install record with status='pending'
	installID := ids.NewULID()
	install := &models.WordPressInstall{
		ID:            installID,
		UserID:        targetUserID,
		DomainID:      req.DomainID,
		DBID:          dbID,
		AdminUsername: req.AdminUsername,
		AdminEmail:    req.AdminEmail,
		Locale:        req.Locale,
		Status:        "pending",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := h.cfg.WordPressInstalls.Create(ctx, install); err != nil {
		// Rollback all
		h.cfg.DatabaseGrants.Delete(ctx, grantID)
		h.cfg.DatabaseUsers.Delete(ctx, dbUserID)
		h.cfg.Databases.Delete(ctx, dbID)
		slog.ErrorContext(ctx, "wordpress create: install row create failed", "err", err, "install_id", installID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Spawn async goroutine to install WordPress
	go createInstallAndKickAgent(ctx, installID, req.DomainID, adminPassword, req.SiteTitle, req.AdminUsername, req.AdminEmail, req.Locale, h.cfg)

	// Return 202 Accepted with plaintext password
	resp := createWordPressResponse{
		ID:            installID,
		DomainID:      req.DomainID,
		DBID:          dbID,
		AdminUsername: req.AdminUsername,
		AdminEmail:    req.AdminEmail,
		AdminPassword: adminPassword,
		Status:        "pending",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	c.JSON(http.StatusAccepted, resp)
}

func (h *wordPressHandler) list(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	page, pageSize, opts := parseListOptions(c, 10, 100)

	ctx := c.Request.Context()
	var installs []models.WordPressInstall
	var total int64
	var err error

	// Admins see all; users see only their own
	if claims.IsAdmin {
		installs, total, err = h.cfg.WordPressInstalls.List(ctx, opts)
	} else {
		installs, total, err = h.cfg.WordPressInstalls.ListByUserID(ctx, claims.UserID, opts)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if installs == nil {
		installs = []models.WordPressInstall{}
	}

	// Batch-lookup domain names so the UI can render them without an
	// N+1 follow-up per row. A user rarely has many installs, and
	// admin "list all" is bounded by pageSize.
	domainNames := make(map[string]string, len(installs))
	for _, inst := range installs {
		if _, ok := domainNames[inst.DomainID]; ok {
			continue
		}
		if d, err := h.cfg.Domains.FindByID(ctx, inst.DomainID); err == nil && d != nil {
			domainNames[inst.DomainID] = d.Name
		}
	}

	out := make([]wordPressListResponse, len(installs))
	for i, inst := range installs {
		out[i] = wordPressListResponse{
			ID:            inst.ID,
			DomainID:      inst.DomainID,
			DomainName:    domainNames[inst.DomainID],
			DBID:          inst.DBID,
			AdminUsername: inst.AdminUsername,
			AdminEmail:    inst.AdminEmail,
			Locale:        inst.Locale,
			Status:        inst.Status,
			Version:       inst.Version,
			LastError:     inst.LastError,
			CreatedAt:     inst.CreatedAt,
			UpdatedAt:     inst.UpdatedAt,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"data":      out,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *wordPressHandler) get(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	installID := c.Param("id")
	ctx := c.Request.Context()

	// Non-admins: check ownership (returns 404 if not owner, not 403)
	var install *models.WordPressInstall
	var err error

	if claims.IsAdmin {
		install, err = h.cfg.WordPressInstalls.FindByID(ctx, installID)
	} else {
		install, err = h.cfg.WordPressInstalls.FindByIDAndUserID(ctx, installID, claims.UserID)
	}

	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "install_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Look up the domain name for consistency with the list response;
	// the UI uses the same row shape for both list and detail.
	var domainName string
	if d, err := h.cfg.Domains.FindByID(ctx, install.DomainID); err == nil && d != nil {
		domainName = d.Name
	}

	resp := wordPressListResponse{
		ID:            install.ID,
		DomainID:      install.DomainID,
		DomainName:    domainName,
		DBID:          install.DBID,
		AdminUsername: install.AdminUsername,
		AdminEmail:    install.AdminEmail,
		Locale:        install.Locale,
		Status:        install.Status,
		Version:       install.Version,
		LastError:     install.LastError,
		CreatedAt:     install.CreatedAt,
		UpdatedAt:     install.UpdatedAt,
	}
	c.JSON(http.StatusOK, resp)
}

func (h *wordPressHandler) delete(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	installID := c.Param("id")
	ctx := c.Request.Context()

	// Ownership check
	var install *models.WordPressInstall
	var err error

	if claims.IsAdmin {
		install, err = h.cfg.WordPressInstalls.FindByID(ctx, installID)
	} else {
		install, err = h.cfg.WordPressInstalls.FindByIDAndUserID(ctx, installID, claims.UserID)
	}

	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "install_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Mark as deleting
	if err := h.cfg.WordPressInstalls.UpdateStatus(ctx, installID, "deleting", nil, nil); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Spawn async goroutine to delete
	go createDeleteAndKickAgent(ctx, installID, install.DBID, h.cfg)

	c.JSON(http.StatusAccepted, gin.H{"status": "deleting"})
}

func (h *wordPressHandler) clone(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	sourceInstallID := c.Param("id")

	var req cloneWordPressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	ctx := c.Request.Context()
	targetUserID := claims.UserID

	// Get source install
	var sourceInstall *models.WordPressInstall
	var err error

	if claims.IsAdmin {
		sourceInstall, err = h.cfg.WordPressInstalls.FindByID(ctx, sourceInstallID)
	} else {
		sourceInstall, err = h.cfg.WordPressInstalls.FindByIDAndUserID(ctx, sourceInstallID, targetUserID)
	}

	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "source_install_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Verify destination domain ownership (403 if cross-user)
	destDomain, err := h.cfg.Domains.FindByID(ctx, req.DestDomainID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if destDomain.UserID != targetUserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Check for existing install on destination domain
	existing, err := h.cfg.WordPressInstalls.FindByDomainID(ctx, req.DestDomainID)
	if err == nil && existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "install_exists"})
		return
	}
	if err != nil && !isNotFound(err) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	now := time.Now().UTC()

	// Provision destination database
	destDBID := ids.NewULID()
	destDBName := "wp_" + destDBID[:6]
	destDatabase := &models.Database{
		ID:        destDBID,
		UserID:    targetUserID,
		Name:      destDBName,
		Engine:    "mariadb",
		Charset:   "utf8mb4",
		Collation: "utf8mb4_unicode_ci",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := h.cfg.Databases.Create(ctx, destDatabase); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Provision destination database user
	destDBUserID := ids.NewULID()
	destDBUsername := "wp_" + destDBUserID[:6]
	plainPassword := ids.NewULID()
	hash, err := bcrypt.GenerateFromPassword([]byte(plainPassword), bcrypt.DefaultCost)
	if err != nil {
		h.cfg.Databases.Delete(ctx, destDBID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	destDatabaseUser := &models.DatabaseUser{
		ID:           destDBUserID,
		UserID:       targetUserID,
		Username:     destDBUsername,
		PasswordHash: string(hash),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.cfg.DatabaseUsers.Create(ctx, destDatabaseUser); err != nil {
		h.cfg.Databases.Delete(ctx, destDBID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Provision grant
	destGrantID := ids.NewULID()
	destGrant := &models.DatabaseUserGrant{
		ID:             destGrantID,
		DatabaseUserID: destDBUserID,
		DatabaseID:     destDBID,
		GrantLevel:     "all",
		Privileges:     "ALL",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := h.cfg.DatabaseGrants.Create(ctx, destGrant); err != nil {
		h.cfg.DatabaseUsers.Delete(ctx, destDBUserID)
		h.cfg.Databases.Delete(ctx, destDBID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Create clone install record
	cloneInstallID := ids.NewULID()
	cloneInstall := &models.WordPressInstall{
		ID:            cloneInstallID,
		UserID:        targetUserID,
		DomainID:      req.DestDomainID,
		DBID:          destDBID,
		AdminUsername: sourceInstall.AdminUsername,
		AdminEmail:    sourceInstall.AdminEmail,
		Locale:        sourceInstall.Locale,
		Status:        "cloning",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := h.cfg.WordPressInstalls.Create(ctx, cloneInstall); err != nil {
		h.cfg.DatabaseGrants.Delete(ctx, destGrantID)
		h.cfg.DatabaseUsers.Delete(ctx, destDBUserID)
		h.cfg.Databases.Delete(ctx, destDBID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Spawn async goroutine to clone
	go createCloneAndKickAgent(ctx, cloneInstallID, sourceInstall.DomainID, req.DestDomainID, destDBID, h.cfg)

	resp := createWordPressResponse{
		ID:            cloneInstallID,
		DomainID:      req.DestDomainID,
		DBID:          destDBID,
		AdminUsername: sourceInstall.AdminUsername,
		AdminEmail:    sourceInstall.AdminEmail,
		Status:        "cloning",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	c.JSON(http.StatusAccepted, resp)
}

func (h *wordPressHandler) health(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	installID := c.Param("id")
	ctx := c.Request.Context()

	// Ownership check
	var err error

	if claims.IsAdmin {
		_, err = h.cfg.WordPressInstalls.FindByID(ctx, installID)
	} else {
		_, err = h.cfg.WordPressInstalls.FindByIDAndUserID(ctx, installID, claims.UserID)
	}

	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "install_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Stub response — Step 5 will define agent endpoint for actual health checks
	// TODO: Implement health check via agent endpoint once WordPress probe is available
	resp := healthResponse{
		WPInstalled: false,
		WPVersion:   "",
		HTTPStatus:  0,
	}
	c.JSON(http.StatusOK, resp)
}

// ---- Async goroutines ----

// createInstallAndKickAgent installs WordPress asynchronously.
// Uses independent context with 5-minute timeout to ensure agent call completes
// even if the original request context is cancelled.
// If panel crashes while installing, the row stays in 'installing' state
// until the reconciler timeout (typically 1 hour) sweeps it as failed.
func createInstallAndKickAgent(parentCtx context.Context, installID, domainID, adminPassword, siteTitle, adminUsername, adminEmail, locale string, cfg WordPressHandlerConfig) {
	// Use independent context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Update status to 'installing'
	if err := cfg.WordPressInstalls.UpdateStatus(ctx, installID, "installing", nil, nil); err != nil {
		// Log but don't fail — status was already 'pending'
		return
	}

	// Call agent to install WordPress
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.WordPressInstalls.UpdateStatus(ctx, installID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "wordpress.install", map[string]any{
		"domain_id":      domainID,
		"site_title":     siteTitle,
		"admin_username": adminUsername,
		"admin_email":    adminEmail,
		"admin_password": adminPassword,
		"locale":         locale,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.WordPressInstalls.UpdateStatus(ctx, installID, "failed", &errMsg, nil)
		return
	}

	// Parse version from response
	var respMap map[string]any
	if err := json.Unmarshal(agentResp, &respMap); err != nil {
		errMsg := truncateError(fmt.Sprintf("failed to parse agent response: %v", err), 1024)
		cfg.WordPressInstalls.UpdateStatus(ctx, installID, "failed", &errMsg, nil)
		return
	}

	version := ""
	if v, ok := respMap["version"].(string); ok {
		version = v
	}

	// Update status to 'ready' with version
	cfg.WordPressInstalls.UpdateStatus(ctx, installID, "ready", nil, &version)
}

// createDeleteAndKickAgent deletes WordPress asynchronously.
func createDeleteAndKickAgent(parentCtx context.Context, installID, databaseID string, cfg WordPressHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.WordPressInstalls.UpdateStatus(ctx, installID, "failed", &errMsg, nil)
		return
	}

	// Call agent to delete WordPress
	_, err := cfg.Agent.Call(ctx, "wordpress.delete", map[string]any{
		"database_id": databaseID,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent delete failed: %v", err), 1024)
		cfg.WordPressInstalls.UpdateStatus(ctx, installID, "failed", &errMsg, nil)
		return
	}

	// Delete the install, database, user, and grants
	cfg.WordPressInstalls.Delete(ctx, installID)
}

// createCloneAndKickAgent clones WordPress asynchronously.
func createCloneAndKickAgent(parentCtx context.Context, cloneInstallID, sourceDomainID, destDomainID, destDatabaseID string, cfg WordPressHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.WordPressInstalls.UpdateStatus(ctx, cloneInstallID, "failed", &errMsg, nil)
		return
	}

	// Call agent to clone WordPress
	agentResp, err := cfg.Agent.Call(ctx, "wordpress.clone", map[string]any{
		"source_domain_id": sourceDomainID,
		"dest_domain_id":   destDomainID,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent clone failed: %v", err), 1024)
		cfg.WordPressInstalls.UpdateStatus(ctx, cloneInstallID, "failed", &errMsg, nil)
		// Attempt cleanup
		cfg.Agent.Call(ctx, "wordpress.delete", map[string]any{
			"database_id": destDatabaseID,
		})
		return
	}

	// Parse version from response
	var respMap map[string]any
	if err := json.Unmarshal(agentResp, &respMap); err != nil {
		errMsg := truncateError(fmt.Sprintf("failed to parse agent response: %v", err), 1024)
		cfg.WordPressInstalls.UpdateStatus(ctx, cloneInstallID, "failed", &errMsg, nil)
		return
	}

	version := ""
	if v, ok := respMap["version"].(string); ok {
		version = v
	}

	// Update status to 'ready' with version
	cfg.WordPressInstalls.UpdateStatus(ctx, cloneInstallID, "ready", nil, &version)
}

// ---- Helpers ----

func truncateError(msg string, maxLen int) string {
	if len(msg) > maxLen {
		return msg[:maxLen]
	}
	return msg
}

func isValidEmail(email string) bool {
	const emailRegex = `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`
	re := regexp.MustCompile(emailRegex)
	return re.MatchString(email)
}
