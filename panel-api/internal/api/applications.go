package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
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

// adminUsernameAlphabet is the lowercase Latin alphabet — same set
// the operator's directive specified ("6 letters auto generated").
// Avoiding digits and uppercase keeps the generated username trivial
// to communicate verbally and matches MediaWiki/WordPress's broad
// username-rules without needing per-app sanitisation.
const adminUsernameAlphabet = "abcdefghijklmnopqrstuvwxyz"

// generateAdminUsername returns n random lowercase letters from
// crypto/rand. Used for every app's admin account so the UI never has
// to ask for a username — see the descriptor schemas which deliberately
// omit admin_username. Falls back to "admin" + ULID-suffix if rand
// fails so the install doesn't 500 on a transient entropy hiccup.
func generateAdminUsername(n int) string {
	if n <= 0 {
		n = 6
	}
	out := make([]byte, n)
	max := big.NewInt(int64(len(adminUsernameAlphabet)))
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			fallback := "admin" + strings.ToLower(ids.NewULID()[:6])
			if len(fallback) > n+5 {
				return fallback[:n+5]
			}
			return fallback
		}
		out[i] = adminUsernameAlphabet[idx.Int64()]
	}
	return string(out)
}

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

	res, cerr := InstallApplication(c.Request.Context(), h.cfg, InstallParams{
		AppType:      req.AppType,
		UserID:       claims.UserID,
		IsAdminCall:  claims.IsAdmin,
		DomainID:     req.DomainID,
		Subdirectory: req.Subdirectory,
		UseWWW:       req.UseWWW,
		Params:       req.Params,
	})
	if cerr != nil {
		body := gin.H{"error": cerr.Code}
		if cerr.Detail != "" {
			body["detail"] = cerr.Detail
		}
		c.JSON(cerr.HTTPStatus, body)
		return
	}

	install := res.Install
	c.JSON(http.StatusAccepted, createApplicationResponse{
		ID:            install.ID,
		AppType:       install.AppType,
		DomainID:      install.DomainID,
		DBID:          install.DBIDOr(),
		AdminUsername: install.AdminUsername,
		AdminPassword: res.AdminPassword,
		AdminEmail:    install.AdminEmail,
		UseWWW:        install.UseWWW,
		Subdirectory:  install.Subdirectory,
		Status:        install.Status,
		CreatedAt:     install.CreatedAt,
		UpdatedAt:     install.UpdatedAt,
	})
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

// dokuWikiKickArgs is what the install-row kicker passes through to
// the agent's dokuwiki installer (via the app.install dispatcher).
// Mirrors installKickArgs except for DB fields, which DokuWiki doesn't
// use, and adds License.
type dokuWikiKickArgs struct {
	InstallID    string
	OSUser       string
	DocRoot      string
	Subdirectory string
	SiteURL      string
	SiteTitle    string
	AdminUser    string
	AdminPass    string
	AdminEmail   string
	License      string
	UseWWW       bool
}

// createDokuWikiInstallAndKickAgent flips the install row to
// "installing", dispatches app.install with app_type="dokuwiki" via
// the agent dispatcher (step 4), and updates the row to "ready" or
// "failed" based on the result. Mirrors createInstallAndKickAgent but
// for the flat-file DokuWiki shape.
func createDokuWikiInstallAndKickAgent(parentCtx context.Context, args dokuWikiKickArgs, cfg ApplicationHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":     "dokuwiki",
		"os_user":      args.OSUser,
		"docroot":      args.DocRoot,
		"subdirectory": args.Subdirectory,
		"site_url":     args.SiteURL,
		"site_title":   args.SiteTitle,
		"admin_user":   args.AdminUser,
		"admin_pass":   args.AdminPass,
		"admin_email":  args.AdminEmail,
		"license":      args.License,
		"use_www":      args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// mediaWikiKickArgs is what the install-row kicker passes through to
// the agent's mediawiki installer (via the app.install dispatcher).
// Mirrors installKickArgs (DB fields included, since MediaWiki is
// RequiresDB=true) plus a Language param.
type mediaWikiKickArgs struct {
	InstallID    string
	OSUser       string
	DocRoot      string
	Subdirectory string
	SiteURL      string
	DBName       string
	DBUser       string
	DBPassword   string
	SiteTitle    string
	AdminUser    string
	AdminPass    string
	AdminEmail   string
	Language     string
	UseWWW       bool
}

// createMediaWikiInstallAndKickAgent flips the install row to
// "installing", dispatches app.install with app_type="mediawiki" via
// the agent dispatcher (step 4), and updates the row to "ready" or
// "failed" based on the result. Mirrors createInstallAndKickAgent +
// createDokuWikiInstallAndKickAgent for the MediaWiki-specific param
// shape.
func createMediaWikiInstallAndKickAgent(parentCtx context.Context, args mediaWikiKickArgs, cfg ApplicationHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":     "mediawiki",
		"os_user":      args.OSUser,
		"docroot":      args.DocRoot,
		"subdirectory": args.Subdirectory,
		"site_url":     args.SiteURL,
		"db_name":      args.DBName,
		"db_user":      args.DBUser,
		"db_password":  args.DBPassword,
		"db_host":      "localhost",
		"site_title":   args.SiteTitle,
		"admin_user":   args.AdminUser,
		"admin_pass":   args.AdminPass,
		"admin_email":  args.AdminEmail,
		"language":     args.Language,
		"use_www":      args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// drupalKickArgs is what the install-row kicker passes through to the
// agent's drupal installer (via the app.install dispatcher). Mirrors
// mediaWikiKickArgs (RequiresDB=true, same DB fields) plus a Profile
// param for drush's install-profile selection and SiteMail for the
// outbound From-address.
type drupalKickArgs struct {
	InstallID    string
	OSUser       string
	DocRoot      string
	Subdirectory string
	SiteURL      string
	DBName       string
	DBUser       string
	DBPassword   string
	SiteTitle    string
	AdminUser    string
	AdminPass    string
	AdminEmail   string
	SiteMail     string
	Profile      string
	UseWWW       bool
}

// createDrupalInstallAndKickAgent flips the install row to "installing",
// dispatches app.install with app_type="drupal" via the agent
// dispatcher, and updates the row to "ready" or "failed" based on the
// result. Mirrors createMediaWikiInstallAndKickAgent for the Drupal-
// specific param shape.
//
// Timeout is 20 minutes — `composer require drush/drush` can take 5-10
// min on a cold cache, then drush site:install another 1-2 min.
func createDrupalInstallAndKickAgent(parentCtx context.Context, args drupalKickArgs, cfg ApplicationHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":     "drupal",
		"os_user":      args.OSUser,
		"docroot":      args.DocRoot,
		"subdirectory": args.Subdirectory,
		"site_url":     args.SiteURL,
		"db_name":      args.DBName,
		"db_user":      args.DBUser,
		"db_password":  args.DBPassword,
		"db_host":      "localhost",
		"site_title":   args.SiteTitle,
		"admin_user":   args.AdminUser,
		"admin_pass":   args.AdminPass,
		"admin_email":  args.AdminEmail,
		"site_mail":    args.SiteMail,
		"profile":      args.Profile,
		"use_www":      args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// joomlaKickArgs is what the install-row kicker passes through to the
// agent's joomla installer (via the app.install dispatcher). Mirrors
// drupalKickArgs minus profile/site_mail; Joomla wants a display name
// (admin_full_name) distinct from the login username.
type joomlaKickArgs struct {
	InstallID     string
	OSUser        string
	DocRoot       string
	Subdirectory  string
	SiteURL       string
	DBName        string
	DBUser        string
	DBPassword    string
	SiteTitle     string
	AdminUser     string
	AdminPass     string
	AdminEmail    string
	AdminFullName string
	UseWWW        bool
}

// createJoomlaInstallAndKickAgent flips the install row to "installing",
// dispatches app.install with app_type="joomla" via the agent
// dispatcher, and updates the row to "ready" or "failed" based on the
// result.
//
// 10-minute timeout — Joomla's tarball is ~30MB and the CLI installer
// runs ~30s on a typical host; no composer chain so much shorter than
// Drupal's 20-min budget.
func createJoomlaInstallAndKickAgent(parentCtx context.Context, args joomlaKickArgs, cfg ApplicationHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":         "joomla",
		"os_user":          args.OSUser,
		"docroot":          args.DocRoot,
		"subdirectory":     args.Subdirectory,
		"site_url":         args.SiteURL,
		"db_name":          args.DBName,
		"db_user":          args.DBUser,
		"db_password":      args.DBPassword,
		"db_host":          "localhost",
		"site_title":       args.SiteTitle,
		"admin_user":       args.AdminUser,
		"admin_pass":       args.AdminPass,
		"admin_email":      args.AdminEmail,
		"admin_full_name":  args.AdminFullName,
		"use_www":          args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// phpbbKickArgs is what the install-row kicker passes through to the
// agent's phpbb installer (via the app.install dispatcher).
type phpbbKickArgs struct {
	InstallID        string
	OSUser           string
	DocRoot          string
	Subdirectory     string
	SiteURL          string
	DBName           string
	DBUser           string
	DBPassword       string
	SiteTitle        string
	BoardDescription string
	AdminUser        string
	AdminPass        string
	AdminEmail       string
	Language         string
	UseWWW           bool
}

// createPhpBBInstallAndKickAgent flips the install row to "installing",
// dispatches app.install with app_type="phpbb" and updates the row
// to "ready" or "failed" based on the result.
func createPhpBBInstallAndKickAgent(parentCtx context.Context, args phpbbKickArgs, cfg ApplicationHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":          "phpbb",
		"os_user":           args.OSUser,
		"docroot":           args.DocRoot,
		"subdirectory":      args.Subdirectory,
		"site_url":          args.SiteURL,
		"db_name":           args.DBName,
		"db_user":           args.DBUser,
		"db_password":       args.DBPassword,
		"db_host":           "localhost",
		"site_title":        args.SiteTitle,
		"board_description": args.BoardDescription,
		"admin_user":        args.AdminUser,
		"admin_pass":        args.AdminPass,
		"admin_email":       args.AdminEmail,
		"language":          args.Language,
		"use_www":           args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// gravKickArgs is what the install-row kicker passes through to the
// agent's grav installer (RequiresDB=false, like DokuWiki — no DB fields).
type gravKickArgs struct {
	InstallID     string
	OSUser        string
	DocRoot       string
	Subdirectory  string
	SiteURL       string
	SiteTitle     string
	AdminUser     string
	AdminPass     string
	AdminEmail    string
	AdminFullName string
	UseWWW        bool
}

func createGravInstallAndKickAgent(parentCtx context.Context, args gravKickArgs, cfg ApplicationHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":         "grav",
		"os_user":          args.OSUser,
		"docroot":          args.DocRoot,
		"subdirectory":     args.Subdirectory,
		"site_url":         args.SiteURL,
		"site_title":       args.SiteTitle,
		"admin_user":       args.AdminUser,
		"admin_pass":       args.AdminPass,
		"admin_email":      args.AdminEmail,
		"admin_full_name":  args.AdminFullName,
		"use_www":          args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// freshrssKickArgs is what the install-row kicker passes through to
// the agent's freshrss installer.
type freshrssKickArgs struct {
	InstallID    string
	OSUser       string
	DocRoot      string
	Subdirectory string
	SiteURL      string
	DBName       string
	DBUser       string
	DBPassword   string
	AdminUser    string
	AdminPass    string
	AdminEmail   string
	Language     string
	UseWWW       bool
}

func createFreshRSSInstallAndKickAgent(parentCtx context.Context, args freshrssKickArgs, cfg ApplicationHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":     "freshrss",
		"os_user":      args.OSUser,
		"docroot":      args.DocRoot,
		"subdirectory": args.Subdirectory,
		"site_url":     args.SiteURL,
		"db_name":      args.DBName,
		"db_user":      args.DBUser,
		"db_password":  args.DBPassword,
		"db_host":      "localhost",
		"admin_user":   args.AdminUser,
		"admin_pass":   args.AdminPass,
		"admin_email":  args.AdminEmail,
		"language":     args.Language,
		"use_www":      args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// matomoKickArgs and createMatomoInstallAndKickAgent — analytics tool.
type matomoKickArgs struct {
	InstallID    string
	OSUser       string
	DocRoot      string
	Subdirectory string
	SiteURL      string
	DBName       string
	DBUser       string
	DBPassword   string
	AdminUser    string
	AdminPass    string
	AdminEmail   string
	UseWWW       bool
}

func createMatomoInstallAndKickAgent(parentCtx context.Context, args matomoKickArgs, cfg ApplicationHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":     "matomo",
		"os_user":      args.OSUser,
		"docroot":      args.DocRoot,
		"subdirectory": args.Subdirectory,
		"site_url":     args.SiteURL,
		"db_name":      args.DBName,
		"db_user":      args.DBUser,
		"db_password":  args.DBPassword,
		"db_host":      "localhost",
		"admin_user":   args.AdminUser,
		"admin_pass":   args.AdminPass,
		"admin_email":  args.AdminEmail,
		"use_www":      args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}
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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// concreteKickArgs and createConcreteInstallAndKickAgent — Concrete CMS.
type concreteKickArgs struct {
	InstallID     string
	OSUser        string
	DocRoot       string
	Subdirectory  string
	SiteURL       string
	DBName        string
	DBUser        string
	DBPassword    string
	SiteTitle     string
	AdminUser     string
	AdminPass     string
	AdminEmail    string
	StartingPoint string
	Locale        string
	UseWWW        bool
}

func createConcreteInstallAndKickAgent(parentCtx context.Context, args concreteKickArgs, cfg ApplicationHandlerConfig) {
	// Concrete's c5:install pulls in starting-point content (sample
	// pages, blocks, themes) which can take 2-3 minutes on slower hosts.
	// 15-min budget keeps headroom.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":       "concrete",
		"os_user":        args.OSUser,
		"docroot":        args.DocRoot,
		"subdirectory":   args.Subdirectory,
		"site_url":       args.SiteURL,
		"db_name":        args.DBName,
		"db_user":        args.DBUser,
		"db_password":    args.DBPassword,
		"db_host":        "localhost",
		"site_title":     args.SiteTitle,
		"admin_user":     args.AdminUser,
		"admin_pass":     args.AdminPass,
		"admin_email":    args.AdminEmail,
		"starting_point": args.StartingPoint,
		"locale":         args.Locale,
		"use_www":        args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}
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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// opencartKickArgs and createOpenCartInstallAndKickAgent — e-commerce.
type opencartKickArgs struct {
	InstallID    string
	OSUser       string
	DocRoot      string
	Subdirectory string
	SiteURL      string
	DBName       string
	DBUser       string
	DBPassword   string
	AdminUser    string
	AdminPass    string
	AdminEmail   string
	UseWWW       bool
}

func createOpenCartInstallAndKickAgent(parentCtx context.Context, args opencartKickArgs, cfg ApplicationHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":     "opencart",
		"os_user":      args.OSUser,
		"docroot":      args.DocRoot,
		"subdirectory": args.Subdirectory,
		"site_url":     args.SiteURL,
		"db_name":      args.DBName,
		"db_user":      args.DBUser,
		"db_password":  args.DBPassword,
		"db_host":      "localhost",
		"admin_user":   args.AdminUser,
		"admin_pass":   args.AdminPass,
		"admin_email":  args.AdminEmail,
		"use_www":      args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}
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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// abantecartKickArgs and createAbanteCartInstallAndKickAgent — e-commerce.
type abantecartKickArgs struct {
	InstallID    string
	OSUser       string
	DocRoot      string
	Subdirectory string
	SiteURL      string
	DBName       string
	DBUser       string
	DBPassword   string
	AdminUser    string
	AdminPass    string
	AdminEmail   string
	UseWWW       bool
}

func createAbanteCartInstallAndKickAgent(parentCtx context.Context, args abantecartKickArgs, cfg ApplicationHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":     "abantecart",
		"os_user":      args.OSUser,
		"docroot":      args.DocRoot,
		"subdirectory": args.Subdirectory,
		"site_url":     args.SiteURL,
		"db_name":      args.DBName,
		"db_user":      args.DBUser,
		"db_password":  args.DBPassword,
		"db_host":      "localhost",
		"admin_user":   args.AdminUser,
		"admin_pass":   args.AdminPass,
		"admin_email":  args.AdminEmail,
		"use_www":      args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}
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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// prestashopKickArgs and createPrestaShopInstallAndKickAgent — e-commerce.
type prestashopKickArgs struct {
	InstallID      string
	OSUser         string
	DocRoot        string
	Subdirectory   string
	SiteURL        string
	DBName         string
	DBUser         string
	DBPassword     string
	SiteTitle      string
	AdminEmail     string
	AdminPass      string
	AdminFirstName string
	AdminLastName  string
	Country        string
	Language       string
	UseWWW         bool
}

func createPrestaShopInstallAndKickAgent(parentCtx context.Context, args prestashopKickArgs, cfg ApplicationHandlerConfig) {
	// 20 min — PrestaShop's schema + sample-catalog import is the long
	// pole; on slower hosts the install can run 8-12 minutes.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":         "prestashop",
		"os_user":          args.OSUser,
		"docroot":          args.DocRoot,
		"subdirectory":     args.Subdirectory,
		"site_url":         args.SiteURL,
		"db_name":          args.DBName,
		"db_user":          args.DBUser,
		"db_password":      args.DBPassword,
		"db_host":          "localhost",
		"site_title":       args.SiteTitle,
		"admin_email":      args.AdminEmail,
		"admin_pass":       args.AdminPass,
		"admin_first_name": args.AdminFirstName,
		"admin_last_name":  args.AdminLastName,
		"country":          args.Country,
		"language":         args.Language,
		"use_www":          args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}
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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// backdropKickArgs and createBackdropInstallAndKickAgent — Drupal 7 fork.
type backdropKickArgs struct {
	InstallID    string
	OSUser       string
	DocRoot      string
	Subdirectory string
	SiteURL      string
	DBName       string
	DBUser       string
	DBPassword   string
	SiteTitle    string
	AdminUser    string
	AdminPass    string
	AdminEmail   string
	Profile      string
	UseWWW       bool
}

func createBackdropInstallAndKickAgent(parentCtx context.Context, args backdropKickArgs, cfg ApplicationHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":     "backdrop",
		"os_user":      args.OSUser,
		"docroot":      args.DocRoot,
		"subdirectory": args.Subdirectory,
		"site_url":     args.SiteURL,
		"db_name":      args.DBName,
		"db_user":      args.DBUser,
		"db_password":  args.DBPassword,
		"db_host":      "localhost",
		"site_title":   args.SiteTitle,
		"admin_user":   args.AdminUser,
		"admin_pass":   args.AdminPass,
		"admin_email":  args.AdminEmail,
		"profile":      args.Profile,
		"use_www":      args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}
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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// moodleKickArgs and createMoodleInstallAndKickAgent — first consumer of
// the M19 managed-data-dir framework. install_id is forwarded to the
// agent so the installer can call ensureManagedDataDir to provision
// moodledata at /home/<user>/<install_id>-data/ outside the docroot.
type moodleKickArgs struct {
	InstallID     string
	OSUser        string
	DocRoot       string
	Subdirectory  string
	SiteURL       string
	DBName        string
	DBUser        string
	DBPassword    string
	SiteTitle     string
	SiteShortName string
	AdminUser     string
	AdminPass     string
	AdminEmail    string
	Language      string
	UseWWW        bool
}

func createMoodleInstallAndKickAgent(parentCtx context.Context, args moodleKickArgs, cfg ApplicationHandlerConfig) {
	// 25 min — Moodle's schema migrations are the long pole, especially
	// the question/quiz tables which add 200+ tables one by one.
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":        "moodle",
		"install_id":      args.InstallID,
		"os_user":         args.OSUser,
		"docroot":         args.DocRoot,
		"subdirectory":    args.Subdirectory,
		"site_url":        args.SiteURL,
		"db_name":         args.DBName,
		"db_user":         args.DBUser,
		"db_password":     args.DBPassword,
		"db_host":         "localhost",
		"site_title":      args.SiteTitle,
		"site_short_name": args.SiteShortName,
		"admin_user":      args.AdminUser,
		"admin_pass":      args.AdminPass,
		"admin_email":     args.AdminEmail,
		"language":        args.Language,
		"use_www":         args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}
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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}

// glpiKickArgs and createGLPIInstallAndKickAgent — IT asset management.
type glpiKickArgs struct {
	InstallID    string
	OSUser       string
	DocRoot      string
	Subdirectory string
	SiteURL      string
	DBName       string
	DBUser       string
	DBPassword   string
	AdminUser    string
	AdminPass    string
	AdminEmail   string
	Language     string
	UseWWW       bool
}

func createGLPIInstallAndKickAgent(parentCtx context.Context, args glpiKickArgs, cfg ApplicationHandlerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	if err := cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "installing", nil, nil); err != nil {
		return
	}
	if cfg.Agent == nil {
		errMsg := "agent not configured"
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}

	agentResp, err := cfg.Agent.Call(ctx, "app.install", map[string]any{
		"app_type":     "glpi",
		"os_user":      args.OSUser,
		"docroot":      args.DocRoot,
		"subdirectory": args.Subdirectory,
		"site_url":     args.SiteURL,
		"db_name":      args.DBName,
		"db_user":      args.DBUser,
		"db_password":  args.DBPassword,
		"db_host":      "localhost",
		"admin_user":   args.AdminUser,
		"admin_pass":   args.AdminPass,
		"admin_email":  args.AdminEmail,
		"language":     args.Language,
		"use_www":      args.UseWWW,
	})
	if err != nil {
		errMsg := truncateError(fmt.Sprintf("agent install failed: %v", err), 1024)
		cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "failed", &errMsg, nil)
		return
	}
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
	cfg.ApplicationInstalls.UpdateStatus(ctx, args.InstallID, "ready", nil, &version)
}
