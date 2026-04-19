package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/apps"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// RegisterApplicationRoutes mounts the M19 generic /applications
// surface. The legacy /wordpress-installs routes registered by
// RegisterWordPressRoutes stay live in parallel through M19; the UI in
// step 5 cuts over to /applications.
//
// list / get / delete / clone delegate to the existing wordPressHandler
// methods because the row shape, ownership checks and rollback chain
// are identical — only create needs descriptor-driven dispatch.
func RegisterApplicationRoutes(g *gin.RouterGroup, cfg ApplicationHandlerConfig) {
	if cfg.Apps == nil {
		// Hard fail at registration time, not first request — a missing
		// registry is a wiring bug, not a runtime condition.
		panic("api.RegisterApplicationRoutes: cfg.Apps is nil")
	}
	h := &applicationsHandler{cfg: cfg}
	wp := &wordPressHandler{cfg: cfg}

	g.GET("/applications/registry", h.registry)

	apps := g.Group("/applications")
	apps.POST("", h.create)
	apps.GET("", wp.list)
	apps.GET("/:id", wp.get)
	apps.DELETE("/:id", wp.delete)
	apps.POST("/:id/clone", wp.clone)
}

type applicationsHandler struct{ cfg ApplicationHandlerConfig }

// registryEntry is the JSON body returned by GET /applications/registry.
// We project the App descriptor onto a dedicated struct so internal
// fields (AgentInstallCmd, etc.) never leak to the UI.
type registryEntry struct {
	Name                 string                     `json:"name"`
	DisplayName          string                     `json:"display_name"`
	Icon                 string                     `json:"icon,omitempty"`
	Description          string                     `json:"description,omitempty"`
	DefaultSubdirectory  string                     `json:"default_subdirectory"`
	RequiresDB           bool                       `json:"requires_db"`
	SupportedPHPVersions []string                   `json:"supported_php_versions,omitempty"`
	InstallParamSchema   map[string]apps.ParamSpec  `json:"install_param_schema,omitempty"`
}

func (h *applicationsHandler) registry(c *gin.Context) {
	descriptors := h.cfg.Apps.List()
	out := make([]registryEntry, 0, len(descriptors))
	for _, d := range descriptors {
		out = append(out, registryEntry{
			Name:                 d.Name,
			DisplayName:          d.DisplayName,
			Icon:                 d.Icon,
			Description:          d.Description,
			DefaultSubdirectory:  d.DefaultSubdirectory,
			RequiresDB:           d.RequiresDB,
			SupportedPHPVersions: d.SupportedPHPVersions,
			InstallParamSchema:   d.InstallParamSchema,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// createApplicationRequest is the M19 generic install request. The
// UI sends app_type to pick a descriptor; params is the per-app
// extension validated against descriptor.InstallParamSchema.
type createApplicationRequest struct {
	AppType      string         `json:"app_type" binding:"required"`
	DomainID     string         `json:"domain_id" binding:"required"`
	Subdirectory string         `json:"subdirectory"`
	UseWWW       bool           `json:"use_www"`
	Params       map[string]any `json:"params"`
}

// createApplicationResponse mirrors createWordPressResponse so the UI
// can render either surface with the same row shape. AppType is added
// so the UI can route the row to the right detail page.
type createApplicationResponse struct {
	ID            string    `json:"id"`
	AppType       string    `json:"app_type"`
	DomainID      string    `json:"domain_id"`
	DBID          string    `json:"db_id"`
	AdminUsername string    `json:"admin_username,omitempty"`
	AdminPassword string    `json:"admin_password,omitempty"`
	AdminEmail    string    `json:"admin_email,omitempty"`
	UseWWW        bool      `json:"use_www"`
	Subdirectory  string    `json:"subdirectory"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (h *applicationsHandler) create(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req createApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	descriptor, ok := h.cfg.Apps.Get(req.AppType)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_app_type", "detail": "unknown app: " + req.AppType})
		return
	}

	if err := validateSubdirectory(req.Subdirectory); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_subdirectory", "detail": err.Error()})
		return
	}

	if err := validateInstallParams(req.Params, descriptor.InstallParamSchema); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_params", "detail": err.Error()})
		return
	}

	ctx := c.Request.Context()

	domain, err := h.cfg.Domains.FindByID(ctx, req.DomainID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		slog.ErrorContext(ctx, "applications create: domain lookup failed", "err", err, "domain_id", req.DomainID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if domain.UserID != claims.UserID {
		if claims.IsAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
		return
	}

	// (domain, subdirectory, app_type) precheck. The composite UNIQUE
	// added in migration 000046 will also catch this at INSERT, but
	// the explicit lookup gives the UI a clean 409 instead of a
	// generic 500 from the DB constraint.
	existing, lookupErr := h.cfg.ApplicationInstalls.FindByDomainAndSubdirectoryAndAppType(ctx, req.DomainID, req.Subdirectory, descriptor.Name)
	if lookupErr == nil && existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "install_exists"})
		return
	}
	if lookupErr != nil && !isNotFound(lookupErr) {
		slog.ErrorContext(ctx, "applications create: existing install lookup failed", "err", lookupErr)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	var osUser string
	if u, uErr := h.cfg.Users.FindByID(ctx, claims.UserID); uErr == nil && u != nil && u.Username != nil {
		osUser = *u.Username
	}
	if osUser == "" {
		slog.ErrorContext(ctx, "applications create: user has no linux username", "user_id", claims.UserID)
		c.JSON(http.StatusConflict, gin.H{"error": "user_not_provisioned"})
		return
	}

	now := time.Now().UTC()

	// Optional DB chain. RequiresDB=false apps (DokuWiki, etc.) skip
	// straight to the install row + agent kick with db_id="".
	var (
		chain         provisionedDB
		adminPassword string
	)
	if descriptor.RequiresDB {
		// Admin password from params (WordPress) or generated here.
		adminPassword, _ = paramString(req.Params, "admin_password")
		if adminPassword == "" {
			adminPassword = ids.NewULID()
		}

		chain, err = provisionDBChain(ctx, h.cfg, claims.UserID, osUser, adminPassword)
		if err != nil {
			slog.ErrorContext(ctx, "applications create: provision db chain", "err", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
			return
		}
	}

	installID := ids.NewULID()
	install := &models.ApplicationInstall{
		ID:            installID,
		UserID:        claims.UserID,
		DomainID:      req.DomainID,
		DBID:          chain.DBID,
		AppType:       descriptor.Name,
		AdminUsername: paramOr(req.Params, "admin_username", ""),
		AdminEmail:    paramOr(req.Params, "admin_email", ""),
		Locale:        paramOr(req.Params, "locale", "en_US"),
		UseWWW:        req.UseWWW,
		Subdirectory:  req.Subdirectory,
		Status:        "pending",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := h.cfg.ApplicationInstalls.Create(ctx, install); err != nil {
		if descriptor.RequiresDB {
			rollbackDBChain(ctx, h.cfg, chain)
		}
		slog.ErrorContext(ctx, "applications create: install row create failed", "err", err, "install_id", installID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Step 3 only ships the WordPress descriptor in the registry, so
	// the install kicker is the existing wordpress.install path. Step 4
	// generalises this into an app.install dispatcher that branches on
	// descriptor.AgentInstallCmd — until then, non-WP apps would land
	// here with no installer, which is why nothing but WordPress is
	// registered yet.
	if descriptor.Name == "wordpress" {
		siteURL := buildSiteURL(domain.Name, req.UseWWW, req.Subdirectory)
		go createInstallAndKickAgent(ctx, installKickArgs{
			InstallID:     installID,
			OSUser:        osUser,
			DocRoot:       domain.DocRoot,
			DBName:        chain.DBName,
			DBUser:        chain.DBUsername,
			DBPassword:    adminPassword,
			SiteURL:       siteURL,
			SiteTitle:     paramOr(req.Params, "site_title", "My WordPress Site"),
			AdminUsername: install.AdminUsername,
			AdminPassword: adminPassword,
			AdminEmail:    install.AdminEmail,
			Locale:        install.Locale,
			Subdirectory:  install.Subdirectory,
			UseWWW:        install.UseWWW,
		}, h.cfg)
	}

	resp := createApplicationResponse{
		ID:            installID,
		AppType:       descriptor.Name,
		DomainID:      req.DomainID,
		DBID:          chain.DBID,
		AdminUsername: install.AdminUsername,
		AdminPassword: adminPassword,
		AdminEmail:    install.AdminEmail,
		UseWWW:        req.UseWWW,
		Subdirectory:  req.Subdirectory,
		Status:        "pending",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	c.JSON(http.StatusAccepted, resp)
}

// validateInstallParams enforces a descriptor's InstallParamSchema
// against the JSON params blob the UI sent. Rejects missing required
// fields, unknown fields, type/regex/enum mismatches, and empty
// strings for required fields. Errors are user-facing — keep them
// concise and field-named.
func validateInstallParams(params map[string]any, schema map[string]apps.ParamSpec) error {
	if len(schema) == 0 {
		// Nothing to validate; allow whatever the caller sent.
		return nil
	}
	for field, spec := range schema {
		raw, present := params[field]
		if !present || raw == nil {
			if spec.Required {
				return fmt.Errorf("missing required param %q", field)
			}
			continue
		}
		if err := validateOne(field, spec, raw); err != nil {
			return err
		}
	}
	for field := range params {
		if _, ok := schema[field]; !ok {
			return fmt.Errorf("unknown param %q", field)
		}
	}
	return nil
}

func validateOne(field string, spec apps.ParamSpec, raw any) error {
	switch spec.Type {
	case "string", "password":
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("param %q: expected string", field)
		}
		if spec.Required && s == "" {
			return fmt.Errorf("param %q: required", field)
		}
		if spec.Pattern != nil {
			re, err := regexp.Compile(*spec.Pattern)
			if err != nil {
				return fmt.Errorf("param %q: invalid pattern", field)
			}
			if !re.MatchString(s) {
				return fmt.Errorf("param %q: does not match pattern", field)
			}
		}
	case "email":
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("param %q: expected string", field)
		}
		if spec.Required && s == "" {
			return fmt.Errorf("param %q: required", field)
		}
		if !isValidEmail(s) {
			return fmt.Errorf("param %q: invalid email", field)
		}
	case "bool":
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("param %q: expected bool", field)
		}
	case "enum":
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("param %q: expected string", field)
		}
		matched := false
		for _, v := range spec.Values {
			if s == v {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("param %q: must be one of %v", field, spec.Values)
		}
	default:
		return fmt.Errorf("param %q: unsupported type %q", field, spec.Type)
	}
	return nil
}

// paramOr returns the string value at key or the default when missing
// or wrong type. Chosen over Sprint(raw) because the UI may send a
// JSON null which `fmt` would render as "<nil>" — clearly worse than
// a sane default.
func paramOr(params map[string]any, key, def string) string {
	if v, ok := paramString(params, key); ok && v != "" {
		return v
	}
	return def
}

func paramString(params map[string]any, key string) (string, bool) {
	if params == nil {
		return "", false
	}
	v, ok := params[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// provisionedDB carries the IDs and MariaDB-side names a successful
// provisionDBChain produced. The install handler holds onto this so a
// later failure (install row insert) can be unwound via rollbackDBChain
// without re-deriving names from suffixes.
type provisionedDB struct {
	DBID       string
	DBUserID   string
	GrantID    string
	DBName     string
	DBUsername string
}

// provisionDBChain mirrors the inline DB-create chain in
// wordPressHandler.create: panel rows + MariaDB CREATE DATABASE/USER/
// GRANT via the agent. Each step rolls back the prior ones on failure
// before returning the error.
func provisionDBChain(ctx context.Context, cfg ApplicationHandlerConfig, userID, osUser, dbPassword string) (provisionedDB, error) {
	now := time.Now().UTC()
	dbID := ids.NewULID()
	dbSuffix := strings.ToLower(dbID[len(dbID)-6:])
	dbName := osUser + "_wp_" + dbSuffix
	database := &models.Database{
		ID:        dbID,
		UserID:    userID,
		Name:      dbName,
		Engine:    "mariadb",
		Charset:   "utf8mb4",
		Collation: "utf8mb4_unicode_ci",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := cfg.Databases.Create(ctx, database); err != nil {
		return provisionedDB{}, fmt.Errorf("database row: %w", err)
	}

	dbUserID := ids.NewULID()
	dbUserSuffix := strings.ToLower(dbUserID[len(dbUserID)-6:])
	dbUsername := osUser + "_wp_" + dbUserSuffix
	hash, err := bcrypt.GenerateFromPassword([]byte(dbPassword), bcrypt.DefaultCost)
	if err != nil {
		cfg.Databases.Delete(ctx, dbID)
		return provisionedDB{}, fmt.Errorf("bcrypt: %w", err)
	}
	databaseUser := &models.DatabaseUser{
		ID:           dbUserID,
		UserID:       userID,
		Username:     dbUsername,
		PasswordHash: string(hash),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := cfg.DatabaseUsers.Create(ctx, databaseUser); err != nil {
		cfg.Databases.Delete(ctx, dbID)
		return provisionedDB{}, fmt.Errorf("database user row: %w", err)
	}

	grantID := ids.NewULID()
	grant := &models.DatabaseUserGrant{
		ID:             grantID,
		DatabaseUserID: dbUserID,
		DatabaseID:     dbID,
		GrantLevel:     "rw",
		Privileges:     "ALL",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := cfg.DatabaseGrants.Create(ctx, grant); err != nil {
		cfg.DatabaseUsers.Delete(ctx, dbUserID)
		cfg.Databases.Delete(ctx, dbID)
		return provisionedDB{}, fmt.Errorf("grant row: %w", err)
	}

	chain := provisionedDB{
		DBID:       dbID,
		DBUserID:   dbUserID,
		GrantID:    grantID,
		DBName:     dbName,
		DBUsername: dbUsername,
	}

	if cfg.Agent != nil {
		agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		if _, acErr := cfg.Agent.Call(agentCtx, "db.create", map[string]any{
			"db_name":   dbName,
			"charset":   "utf8mb4",
			"collation": "utf8mb4_unicode_ci",
		}); acErr != nil {
			rollbackDBChain(ctx, cfg, chain)
			return provisionedDB{}, fmt.Errorf("agent db.create: %w", acErr)
		}

		if _, acErr := cfg.Agent.Call(agentCtx, "db_user.create", map[string]any{
			"db_user_name": dbUsername,
			"password":     dbPassword,
		}); acErr != nil {
			cfg.Agent.Call(ctx, "db.drop", map[string]any{"db_name": dbName})
			rollbackDBChain(ctx, cfg, chain)
			return provisionedDB{}, fmt.Errorf("agent db_user.create: %w", acErr)
		}

		if _, acErr := cfg.Agent.Call(agentCtx, "db_user.grant", map[string]any{
			"db_name":      dbName,
			"db_user_name": dbUsername,
			"grant_level":  "rw",
			"privileges":   []string{"ALL"},
		}); acErr != nil {
			cfg.Agent.Call(ctx, "db_user.drop", map[string]any{"db_user_name": dbUsername})
			cfg.Agent.Call(ctx, "db.drop", map[string]any{"db_name": dbName})
			rollbackDBChain(ctx, cfg, chain)
			return provisionedDB{}, fmt.Errorf("agent db_user.grant: %w", acErr)
		}
	}

	return chain, nil
}

// rollbackDBChain unwinds a successful provisionDBChain when a later
// step (install row create) fails. Best-effort: agent calls are
// fire-and-forget, panel rows are deleted in child→parent order so
// FK on grants doesn't block.
func rollbackDBChain(ctx context.Context, cfg ApplicationHandlerConfig, chain provisionedDB) {
	if chain.DBID == "" {
		return
	}
	if cfg.Agent != nil {
		cfg.Agent.Call(ctx, "db_user.drop", map[string]any{"db_user_name": chain.DBUsername})
		cfg.Agent.Call(ctx, "db.drop", map[string]any{"db_name": chain.DBName})
	}
	cfg.DatabaseGrants.Delete(ctx, chain.GrantID)
	cfg.DatabaseUsers.Delete(ctx, chain.DBUserID)
	cfg.Databases.Delete(ctx, chain.DBID)
}
