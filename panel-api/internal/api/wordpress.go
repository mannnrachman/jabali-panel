package api

import (
	"context"
	"log/slog"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/apps"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// ApplicationHandlerConfig bundles the repositories and services every
// per-app HTTP handler needs. M19 generalised this from the old
// WordPressHandlerConfig — the legacy WordPress routes still see the
// same struct via the type alias below, so existing wiring + tests
// compile unchanged through the M19 release window.
type ApplicationHandlerConfig struct {
	ApplicationInstalls repository.ApplicationInstallRepository
	Databases           repository.DatabaseRepository
	DatabaseUsers       repository.DatabaseUserRepository
	DatabaseGrants      repository.DatabaseUserGrantRepository
	Domains             repository.DomainRepository
	Users               repository.UserRepository
	Packages            repository.PackageRepository
	Agent               agent.AgentInterface
	// Apps is the M19 application registry. Nil-safe: the legacy
	// /wordpress-installs handlers in this file don't read it (they
	// hard-code the WordPress shape); only the new /applications
	// handlers in applications.go require it. app.NewWithDeps always
	// populates it for production wiring.
	Apps *apps.Registry
}

// WordPressHandlerConfig is the pre-M19 alias retained so old wiring
// (panel-api/cmd/server/serve.go, wordpress_test.go fixtures) compiles
// unchanged. M19.1 deletes the alias once every caller has switched.
type WordPressHandlerConfig = ApplicationHandlerConfig

// RegisterWordPressRoutes registers the legacy /wordpress-installs
// routes. M19 added the parallel /applications surface — see
// RegisterApplicationRoutes in applications.go. Both surfaces remain
// mounted through M19; the UI in step 5 cuts over to /applications.
func RegisterWordPressRoutes(g *gin.RouterGroup, cfg ApplicationHandlerConfig) {
	h := &wordPressHandler{cfg: cfg}

	installs := g.Group("/wordpress-installs")
	installs.POST("", h.create)
	installs.GET("", h.list)
	installs.GET("/:id", h.get)
	installs.DELETE("/:id", h.delete)
	installs.POST("/:id/clone", h.clone)
	installs.POST("/:id/health", h.health)
}

type wordPressHandler struct{ cfg ApplicationHandlerConfig }

// ---- Request/Response types ----

type createWordPressRequest struct {
	DomainID      string `json:"domain_id" binding:"required"`
	SiteTitle     string `json:"site_title" binding:"required"`
	AdminUsername string `json:"admin_username" binding:"required"`
	AdminEmail    string `json:"admin_email" binding:"required"`
	AdminPassword string `json:"admin_password"`
	Locale        string `json:"locale"`
	UseWWW        bool   `json:"use_www"`
	Subdirectory  string `json:"subdirectory"`
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
	UseWWW        bool      `json:"use_www"`
	Subdirectory  string    `json:"subdirectory"`
	Status        string    `json:"status"`
	Version       *string   `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type wordPressListResponse struct {
	ID string `json:"id"`
	// AppType is the M19 discriminator used by the UI's App column to
	// render the right icon + label. Pre-M19 rows in
	// application_installs default to "wordpress" via the column
	// default; the UI also falls back to "wordpress" when the field is
	// missing, so an older API build serving this struct without
	// AppType still renders sanely (just always as WordPress).
	AppType       string    `json:"app_type"`
	DomainID      string    `json:"domain_id"`
	DomainName    string    `json:"domain_name"`
	DBID          string    `json:"db_id"`
	AdminUsername string    `json:"admin_username"`
	AdminEmail    string    `json:"admin_email"`
	Locale        string    `json:"locale"`
	UseWWW        bool      `json:"use_www"`
	Subdirectory  string    `json:"subdirectory"`
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


// subdirectoryRegex accepts a single path segment: starts with
// lowercase alnum, may contain lowercase alnum plus _ or -, max 64
// chars. No slashes, no dots, no uppercase — prevents traversal and
// keeps the docroot layout sane.
var subdirectoryRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Reserved subdirectory names would shadow WordPress core directories
// and make the install immediately broken.
var reservedSubdirectories = map[string]struct{}{
	"wp-admin":    {},
	"wp-includes": {},
	"wp-content":  {},
}

// validateSubdirectory returns nil for empty (subdirectory is optional).
// Non-empty input must match subdirectoryRegex and not be reserved.
func validateSubdirectory(s string) error {
	if s == "" {
		return nil
	}
	if !subdirectoryRegex.MatchString(s) {
		return fmt.Errorf("subdirectory must match ^[a-z0-9][a-z0-9_-]{0,63}$")
	}
	if _, reserved := reservedSubdirectories[strings.ToLower(s)]; reserved {
		return fmt.Errorf("subdirectory is reserved by WordPress core")
	}
	return nil
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

	// Validate optional subdirectory
	if err := validateSubdirectory(req.Subdirectory); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_subdirectory", "detail": err.Error()})
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

	// Check for duplicate install at the same (domain, subdirectory). The
	// pair is unique on disk — same domain can host many installs but each
	// must live at a distinct subdirectory ("" = docroot). Was a per-domain
	// check; that blocked the obvious "main site at root + /blog" pattern.
	existing, err := h.cfg.ApplicationInstalls.FindByDomainAndSubdirectory(ctx, req.DomainID, req.Subdirectory)
	if err == nil && existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "install_exists"})
		return
	}
	if err != nil && !isNotFound(err) {
		slog.ErrorContext(ctx, "wordpress create: existing install lookup failed", "err", err, "domain_id", req.DomainID, "subdirectory", req.Subdirectory)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Resolve the domain owner's linux username. Required because the DB
	// name and DB user name are prefixed with it (panel-wide convention,
	// see databases.go), and the agent needs it for systemd-run targeting.
	var osUser string
	if u, uErr := h.cfg.Users.FindByID(ctx, targetUserID); uErr == nil && u != nil && u.Username != nil {
		osUser = *u.Username
	}
	if osUser == "" {
		slog.ErrorContext(ctx, "wordpress create: user has no linux username", "user_id", targetUserID)
		c.JSON(http.StatusConflict, gin.H{"error": "user_not_provisioned"})
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
	// ULID first chars are timestamp; use the trailing random segment
	// so back-to-back installs do not collide. Lowercase it because
	// MariaDB db/user name validators in panel-agent accept only
	// [a-z0-9_-] and ULIDs are Crockford base32 (uppercase).
	dbSuffix := strings.ToLower(dbID[len(dbID)-6:])
	dbName := osUser + "_wp_" + dbSuffix
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
	dbUserSuffix := strings.ToLower(dbUserID[len(dbUserID)-6:])
	dbUsername := osUser + "_wp_" + dbUserSuffix
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
		GrantLevel:       "rw",
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

	// Provision the MariaDB side via the agent: CREATE DATABASE, CREATE
	// USER, GRANT. Mirrors what the /databases and /database-users API
	// paths do — previously the WP install kicked wordpress.install
	// without ever creating the real DB/user, so wp core install hit
	// "Error establishing a database connection".
	if h.cfg.Agent != nil {
		agentCtx, agentCancel := context.WithTimeout(ctx, 30*time.Second)
		defer agentCancel()

		rollbackPanelRows := func() {
			h.cfg.DatabaseGrants.Delete(ctx, grantID)
			h.cfg.DatabaseUsers.Delete(ctx, dbUserID)
			h.cfg.Databases.Delete(ctx, dbID)
		}

		if _, acErr := h.cfg.Agent.Call(agentCtx, "db.create", map[string]any{
			"db_name":   dbName,
			"charset":   "utf8mb4",
			"collation": "utf8mb4_unicode_ci",
		}); acErr != nil {
			rollbackPanelRows()
			slog.ErrorContext(ctx, "wordpress create: agent db.create", "err", acErr, "db_name", dbName)
			c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": acErr.Error()})
			return
		}

		if _, acErr := h.cfg.Agent.Call(agentCtx, "db_user.create", map[string]any{
			"db_user_name": dbUsername,
			"password":     adminPassword,
		}); acErr != nil {
			// Roll back the MariaDB db we just created.
			h.cfg.Agent.Call(ctx, "db.drop", map[string]any{"db_name": dbName})
			rollbackPanelRows()
			slog.ErrorContext(ctx, "wordpress create: agent db_user.create", "err", acErr, "db_user", dbUsername)
			c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": acErr.Error()})
			return
		}

		if _, acErr := h.cfg.Agent.Call(agentCtx, "db_user.grant", map[string]any{
			"db_name":      dbName,
			"db_user_name": dbUsername,
			"grant_level":  "rw",
			"privileges":   []string{"ALL"},
		}); acErr != nil {
			h.cfg.Agent.Call(ctx, "db_user.drop", map[string]any{"db_user_name": dbUsername})
			h.cfg.Agent.Call(ctx, "db.drop", map[string]any{"db_name": dbName})
			rollbackPanelRows()
			slog.ErrorContext(ctx, "wordpress create: agent db_user.grant", "err", acErr)
			c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": acErr.Error()})
			return
		}
	}

	// Create WordPress install record with status='pending'
	installID := ids.NewULID()
	install := &models.WordPressInstall{
		ID:            installID,
		UserID:        targetUserID,
		DomainID:      req.DomainID,
		DBID:          models.DBIDPtr(dbID),
		AdminUsername: req.AdminUsername,
		AdminEmail:    req.AdminEmail,
		Locale:        req.Locale,
		UseWWW:        req.UseWWW,
		Subdirectory:  req.Subdirectory,
		Status:        "pending",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := h.cfg.ApplicationInstalls.Create(ctx, install); err != nil {
		// Rollback all
		h.cfg.DatabaseGrants.Delete(ctx, grantID)
		h.cfg.DatabaseUsers.Delete(ctx, dbUserID)
		h.cfg.Databases.Delete(ctx, dbID)
		slog.ErrorContext(ctx, "wordpress create: install row create failed", "err", err, "install_id", installID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Compute site URL from (domain, use_www, subdirectory).
	siteURL := buildSiteURL(domain.Name, req.UseWWW, req.Subdirectory)

	// Spawn async goroutine to install WordPress.
	kickArgs := installKickArgs{
		InstallID:     installID,
		OSUser:        osUser,
		DocRoot:       domain.DocRoot,
		DBName:        dbName,
		DBUser:        dbUsername,
		DBPassword:    adminPassword,
		SiteURL:       siteURL,
		SiteTitle:     req.SiteTitle,
		AdminUsername: req.AdminUsername,
		AdminPassword: adminPassword,
		AdminEmail:    req.AdminEmail,
		Locale:        req.Locale,
		Subdirectory:  req.Subdirectory,
		UseWWW:        req.UseWWW,
	}
	go createInstallAndKickAgent(ctx, kickArgs, h.cfg)

	// Return 202 Accepted with plaintext password
	resp := createWordPressResponse{
		ID:            installID,
		DomainID:      req.DomainID,
		DBID:          dbID,
		AdminUsername: req.AdminUsername,
		AdminEmail:    req.AdminEmail,
		AdminPassword: adminPassword,
		UseWWW:        req.UseWWW,
		Subdirectory:  req.Subdirectory,
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
		installs, total, err = h.cfg.ApplicationInstalls.List(ctx, opts)
	} else {
		installs, total, err = h.cfg.ApplicationInstalls.ListByUserID(ctx, claims.UserID, opts)
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
		appType := inst.AppType
		if appType == "" {
			appType = "wordpress" // pre-M19 row safety net
		}
		out[i] = wordPressListResponse{
			ID:            inst.ID,
			AppType:       appType,
			DomainID:      inst.DomainID,
			DomainName:    domainNames[inst.DomainID],
			DBID:          inst.DBIDOr(),
			AdminUsername: inst.AdminUsername,
			AdminEmail:    inst.AdminEmail,
			Locale:        inst.Locale,
			UseWWW:        inst.UseWWW,
			Subdirectory:  inst.Subdirectory,
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
		install, err = h.cfg.ApplicationInstalls.FindByID(ctx, installID)
	} else {
		install, err = h.cfg.ApplicationInstalls.FindByIDAndUserID(ctx, installID, claims.UserID)
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

	getAppType := install.AppType
	if getAppType == "" {
		getAppType = "wordpress"
	}
	resp := wordPressListResponse{
		ID:            install.ID,
		AppType:       getAppType,
		DomainID:      install.DomainID,
		DomainName:    domainName,
		DBID:          install.DBIDOr(),
		AdminUsername: install.AdminUsername,
		AdminEmail:    install.AdminEmail,
		Locale:        install.Locale,
		UseWWW:        install.UseWWW,
		Subdirectory:  install.Subdirectory,
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
		install, err = h.cfg.ApplicationInstalls.FindByID(ctx, installID)
	} else {
		install, err = h.cfg.ApplicationInstalls.FindByIDAndUserID(ctx, installID, claims.UserID)
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
	if err := h.cfg.ApplicationInstalls.UpdateStatus(ctx, installID, "deleting", nil, nil); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Resolve os_user + docroot so the agent knows what to clean up.
	domain, err := h.cfg.Domains.FindByID(ctx, install.DomainID)
	if err != nil {
		slog.ErrorContext(ctx, "wordpress delete: domain lookup", "err", err, "domain_id", install.DomainID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	var osUser string
	if u, uErr := h.cfg.Users.FindByID(ctx, install.UserID); uErr == nil && u != nil && u.Username != nil {
		osUser = *u.Username
	}
	if osUser == "" {
		slog.ErrorContext(ctx, "wordpress delete: user has no linux username", "user_id", install.UserID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	dbUserID := ""
	var dbUserUsername string
	if grants, gerr := h.cfg.DatabaseGrants.ListByDatabaseID(ctx, install.DBIDOr()); gerr == nil && len(grants) > 0 {
		dbUserID = grants[0].DatabaseUserID
		if dbu, duErr := h.cfg.DatabaseUsers.FindByID(ctx, dbUserID); duErr == nil && dbu != nil {
			dbUserUsername = dbu.Username
		}
	}

	// Spawn async goroutine to delete. Pass the AppType + Subdirectory
	// from the install row so the kicker dispatches to the right per-
	// app deleter (was hardcoded to "wordpress" pre-M19, which silently
	// routed Drupal/Joomla/etc deletes to the WP file-list cleaner).
	// install_id is plumbed through so deleters that opt into the
	// managed-data-dir contract (Moodle/GLPI/Chamilo) can recompute the
	// /home/<user>/<install_id>-data path and rm it.
	go createDeleteAndKickAgent(ctx, installID, install.AppType, install.Subdirectory, install.DBIDOr(), dbUserID, osUser, domain.DocRoot, domain.Name, dbUserUsername, h.cfg)

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
		sourceInstall, err = h.cfg.ApplicationInstalls.FindByID(ctx, sourceInstallID)
	} else {
		sourceInstall, err = h.cfg.ApplicationInstalls.FindByIDAndUserID(ctx, sourceInstallID, targetUserID)
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

	// Check for existing install at the same (dest_domain, source_subdir).
	// Clone preserves the source install's subdirectory, so collision only
	// happens if the destination already hosts an install at that same
	// subdir — sibling installs at other subdirs are fine.
	existing, err := h.cfg.ApplicationInstalls.FindByDomainAndSubdirectory(ctx, req.DestDomainID, sourceInstall.Subdirectory)
	if err == nil && existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "install_exists"})
		return
	}
	if err != nil && !isNotFound(err) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	now := time.Now().UTC()

	// Resolve the domain owner's linux username — same prefix convention
	// as the install path uses. Required for DB naming and future systemd-run
	// targeting.
	var osUser string
	if u, uErr := h.cfg.Users.FindByID(ctx, targetUserID); uErr == nil && u != nil && u.Username != nil {
		osUser = *u.Username
	}
	if osUser == "" {
		slog.ErrorContext(ctx, "wordpress clone: user has no linux username", "user_id", targetUserID)
		c.JSON(http.StatusConflict, gin.H{"error": "user_not_provisioned"})
		return
	}

	// Provision destination database
	destDBID := ids.NewULID()
	destDBSuffix := strings.ToLower(destDBID[len(destDBID)-6:])
	destDBName := osUser + "_wp_" + destDBSuffix
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
	destDBUserSuffix := strings.ToLower(destDBUserID[len(destDBUserID)-6:])
	destDBUsername := osUser + "_wp_" + destDBUserSuffix
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
		GrantLevel:     "rw",
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

	// Provision the MariaDB side via the agent: CREATE DATABASE, CREATE
	// USER, GRANT — same pattern as the install handler (ba17cd7). Without
	// this, the clone lands a panel row that points at a non-existent
	// MariaDB database and wp core install/clone bombs with
	// "Error establishing a database connection".
	if h.cfg.Agent != nil {
		agentCtx, agentCancel := context.WithTimeout(ctx, 30*time.Second)
		defer agentCancel()

		rollbackPanelRows := func() {
			h.cfg.DatabaseGrants.Delete(ctx, destGrantID)
			h.cfg.DatabaseUsers.Delete(ctx, destDBUserID)
			h.cfg.Databases.Delete(ctx, destDBID)
		}

		if _, acErr := h.cfg.Agent.Call(agentCtx, "db.create", map[string]any{
			"db_name":   destDBName,
			"charset":   "utf8mb4",
			"collation": "utf8mb4_unicode_ci",
		}); acErr != nil {
			rollbackPanelRows()
			slog.ErrorContext(ctx, "wordpress clone: agent db.create", "err", acErr, "db_name", destDBName)
			c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": acErr.Error()})
			return
		}

		if _, acErr := h.cfg.Agent.Call(agentCtx, "db_user.create", map[string]any{
			"db_user_name": destDBUsername,
			"password":     plainPassword,
		}); acErr != nil {
			h.cfg.Agent.Call(ctx, "db.drop", map[string]any{"db_name": destDBName})
			rollbackPanelRows()
			slog.ErrorContext(ctx, "wordpress clone: agent db_user.create", "err", acErr, "db_user", destDBUsername)
			c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": acErr.Error()})
			return
		}

		if _, acErr := h.cfg.Agent.Call(agentCtx, "db_user.grant", map[string]any{
			"db_name":      destDBName,
			"db_user_name": destDBUsername,
			"grant_level":  "rw",
			"privileges":   []string{"ALL"},
		}); acErr != nil {
			h.cfg.Agent.Call(ctx, "db_user.drop", map[string]any{"db_user_name": destDBUsername})
			h.cfg.Agent.Call(ctx, "db.drop", map[string]any{"db_name": destDBName})
			rollbackPanelRows()
			slog.ErrorContext(ctx, "wordpress clone: agent db_user.grant", "err", acErr)
			c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": acErr.Error()})
			return
		}
	}

	// Create clone install record
	cloneInstallID := ids.NewULID()
	cloneInstall := &models.WordPressInstall{
		ID:            cloneInstallID,
		UserID:        targetUserID,
		DomainID:      req.DestDomainID,
		DBID:          models.DBIDPtr(destDBID),
		AdminUsername: sourceInstall.AdminUsername,
		AdminEmail:    sourceInstall.AdminEmail,
		Locale:        sourceInstall.Locale,
		UseWWW:        sourceInstall.UseWWW,
		Subdirectory:  sourceInstall.Subdirectory,
		Status:        "cloning",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := h.cfg.ApplicationInstalls.Create(ctx, cloneInstall); err != nil {
		h.cfg.DatabaseGrants.Delete(ctx, destGrantID)
		h.cfg.DatabaseUsers.Delete(ctx, destDBUserID)
		h.cfg.Databases.Delete(ctx, destDBID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Spawn async goroutine to clone
	go createCloneAndKickAgent(ctx, cloneInstallID, sourceInstall.DomainID, req.DestDomainID, destDBID, sourceInstall.Subdirectory, sourceInstall.UseWWW, h.cfg)

	resp := createWordPressResponse{
		ID:            cloneInstallID,
		DomainID:      req.DestDomainID,
		DBID:          destDBID,
		AdminUsername: sourceInstall.AdminUsername,
		AdminEmail:    sourceInstall.AdminEmail,
		UseWWW:        sourceInstall.UseWWW,
		Subdirectory:  sourceInstall.Subdirectory,
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
		_, err = h.cfg.ApplicationInstalls.FindByID(ctx, installID)
	} else {
		_, err = h.cfg.ApplicationInstalls.FindByIDAndUserID(ctx, installID, claims.UserID)
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
// installKickArgs bundles everything createInstallAndKickAgent needs.
// It exists because the agent contract has many required fields and we
// do not want a 14-arg function signature.
type installKickArgs struct {
	InstallID     string
	OSUser        string
	DocRoot       string
	DBName        string
	DBUser        string
	DBPassword    string
	SiteURL       string
	SiteTitle     string
	AdminUsername string
	AdminPassword string
	AdminEmail    string
	Locale        string
	Subdirectory  string
	UseWWW        bool
}

// buildSiteURL composes the canonical WordPress siteurl/home value.
// Matches the rule used by the agent when use_www is true / subdirectory
// is set.
func buildSiteURL(domain string, useWWW bool, subdirectory string) string {
	host := domain
	if useWWW {
		host = "www." + domain
	}
	u := "https://" + host
	if subdirectory != "" {
		u += "/" + subdirectory
	}
	return u
}

func createInstallAndKickAgent(parentCtx context.Context, args installKickArgs, cfg WordPressHandlerConfig) {
	// Use independent context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Update status to 'installing'
	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		// Log but don't fail — status was already 'pending'
		return
	}

	// Call agent to install WordPress
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	// M19: dispatched through app.install with app_type discriminator.
	// The agent's app dispatcher forwards the body unchanged to the
	// registered "wordpress" installer (see panel-agent/internal/commands/
	// app_dispatch.go). Legacy "wordpress.install" still works on the
	// agent for any straggler caller through M19.1.
	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":      "wordpress",
		"os_user":       args.OSUser,
		"docroot":       args.DocRoot,
		"db_name":       args.DBName,
		"db_user":       args.DBUser,
		"db_password":   args.DBPassword,
		"db_host":       "localhost",
		"site_url":      args.SiteURL,
		"site_title":    args.SiteTitle,
		"admin_user":    args.AdminUsername,
		"admin_pass":    args.AdminPassword,
		"admin_email":   args.AdminEmail,
		"locale":        args.Locale,
		"subdirectory":  args.Subdirectory,
		"use_www":       args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	// Parse version from response
	var respMap map[string]any
	if err := json.Unmarshal(agentResp, &respMap); err != nil {
		errMsg := truncateError(fmt.Sprintf("failed to parse agent response: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	version := ""
	if v, ok := respMap["version"].(string); ok {
		version = v
	}

	// Update status to 'ready' with version
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// createDeleteAndKickAgent removes the on-disk WordPress files via the
// agent and cleans up every DB row tied to the install (database,
// database user, grants, install record). If the agent file-removal
// fails we flip status to failed but still allow a future retry.
// Non-empty osUser+docroot are required; the handler pre-fills them.
func createDeleteAndKickAgent(parentCtx context.Context, installID, appType, subdirectory, databaseID, dbUserID, osUser, docroot, domainName, dbUserUsername string, cfg WordPressHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, installID, "failed", &errMsg, nil)
		return
	}

	// Default appType to "wordpress" for any install row that pre-dates
	// the M19 migration (the column NOT NULL DEFAULT 'wordpress' should
	// have backfilled, but treat empty defensively).
	if appType == "" {
		appType = "wordpress"
	}

	// Agent removes the app's files from the docroot AND restores
	// the domain.create placeholder index.html (when domain is non-empty)
	// so the docroot doesn't 403 after delete. Does NOT touch the MySQL
	// side — panel handles that below.
	//
	// M19: dispatched through app.delete with app_type discriminator
	// (was hardcoded "wordpress" before the rename).
	// install_id + subdirectory let deleters that opted into the
	// managed-data-dir contract recompute /home/<user>/<install_id>-data
	// for cleanup, and let subdir installs target the right path.
	_, err := cfg.Agent.Call(ctx, "app.delete", map[string]any{
		"app_type":     appType,
		"install_id":   installID,
		"os_user":      osUser,
		"docroot":      docroot,
		"subdirectory": subdirectory,
		"domain":       domainName,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent delete failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, installID, "failed", &errMsg, nil)
		return
	}

	// Best-effort DB-side cleanup. Drop the mariadb database + user on the
	// host via the agent and remove the panel-side rows so the slot is
	// freed up. Order matters:
	//   grants → users → wp install row → database
	// because fk_wpinstalls_db is RESTRICT — deleting the databases row
	// before the wordpress_installs row that references it fails (silently,
	// since the error is swallowed) and leaves an orphan databases row.
	if dbUserID != "" {
		if grants, gErr := cfg.DatabaseGrants.ListByDatabaseUserID(ctx, dbUserID); gErr == nil {
			for _, g := range grants {
				cfg.DatabaseGrants.Delete(ctx, g.ID)
			}
		}
		// Drop the mariadb user on the host. The agent command is
		// `db_user.drop` with `db_user_name` — NOT `mysql.user.delete`
		// (which doesn't exist; the prior call silently no-op'd and the
		// account survived every WP delete).
		if dbUserUsername != "" {
			if _, agentErr := cfg.Agent.Call(ctx, "db_user.drop", map[string]any{"db_user_name": dbUserUsername}); agentErr != nil {
				slog.WarnContext(ctx, "wordpress delete: db_user.drop failed", "err", agentErr, "db_user", dbUserUsername)
			}
		}
		cfg.DatabaseUsers.Delete(ctx, dbUserID)
	}
	// Delete the WP install row BEFORE the database panel row so the
	// fk_wpinstalls_db RESTRICT constraint releases.
	cfg.ApplicationInstalls.Delete(ctx, installID)
	if databaseID != "" {
		if db, dbErr := cfg.Databases.FindByID(ctx, databaseID); dbErr == nil && db != nil {
			// Agent command is `db.drop` with `db_name` — NOT
			// `mysql.database.delete` (same silent-no-op bug as above,
			// the schema survived every delete).
			if _, agentErr := cfg.Agent.Call(ctx, "db.drop", map[string]any{"db_name": db.Name}); agentErr != nil {
				slog.WarnContext(ctx, "wordpress delete: db.drop failed", "err", agentErr, "db_name", db.Name)
			}
		}
		cfg.Databases.Delete(ctx, databaseID)
	}
}

// createCloneAndKickAgent clones WordPress asynchronously.
func createCloneAndKickAgent(parentCtx context.Context, cloneInstallID, sourceDomainID, destDomainID, destDatabaseID, dstSubdirectory string, useWWW bool, cfg WordPressHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, cloneInstallID, "failed", &errMsg, nil)
		return
	}

	// M19: dispatched through app.clone with app_type discriminator.
	agentResp, err := cfg.Agent.Call(ctx, "app.clone", map[string]any{
		"app_type":         "wordpress",
		"source_domain_id": sourceDomainID,
		"dest_domain_id":   destDomainID,
		"use_www":          useWWW,
		"dst_subdirectory": dstSubdirectory,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent clone failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, cloneInstallID, "failed", &errMsg, nil)
		// Best-effort cleanup; same dispatcher path.
		cfg.Agent.Call(ctx, "app.delete", map[string]any{
			"app_type":    "wordpress",
			"database_id": destDatabaseID,
		})
		return
	}

	// Parse version from response
	var respMap map[string]any
	if err := json.Unmarshal(agentResp, &respMap); err != nil {
		errMsg := truncateError(fmt.Sprintf("failed to parse agent response: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, cloneInstallID, "failed", &errMsg, nil)
		return
	}

	version := ""
	if v, ok := respMap["version"].(string); ok {
		version = v
	}

	// Update status to 'ready' with version
	cfg.ApplicationInstalls.UpdateStatus(ctx, cloneInstallID, "ready", nil, &version)
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
