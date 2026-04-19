package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
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

	// (domain, subdirectory) precheck — regardless of app_type. Per
	// the operator's directive, a (domain, subdir) slot may host AT
	// MOST ONE application; you can't install MediaWiki at / when a
	// WordPress already lives there. The DB-level UNIQUE in migration
	// 000046 still includes app_type for forward compat, so this API
	// check is what enforces the stricter product rule.
	existing, lookupErr := h.cfg.ApplicationInstalls.FindByDomainAndSubdirectory(ctx, req.DomainID, req.Subdirectory)
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

	// Always generate the admin username server-side. The descriptor
	// schema deliberately omits admin_username so the UI never asks
	// (per operator's "admin username is a bad idea, 6 letters auto
	// generated"). For MediaWiki we uppercase the first letter to
	// satisfy its "username must start with capital" rule.
	adminUsername := generateAdminUsername(6)
	if descriptor.Name == "mediawiki" && len(adminUsername) > 0 {
		adminUsername = strings.ToUpper(adminUsername[:1]) + adminUsername[1:]
	}

	installID := ids.NewULID()
	install := &models.ApplicationInstall{
		ID:            installID,
		UserID:        claims.UserID,
		DomainID:      req.DomainID,
		DBID:          chain.DBID,
		AppType:       descriptor.Name,
		AdminUsername: adminUsername,
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

	// Per-app install kicker. The agent dispatcher (step 4) routes
	// app.install by app_type, so the panel just needs to assemble the
	// per-app param shape and fire the kick. Adding an app means adding
	// a case here + the descriptor + the agent installer.
	siteURL := buildSiteURL(domain.Name, req.UseWWW, req.Subdirectory)
	switch descriptor.Name {
	case "wordpress":
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
	case "dokuwiki":
		// adminPassword is empty here when the descriptor's
		// admin_password param is optional and the user left it blank;
		// the kicker generates one and surfaces it via the install row's
		// reveal-once panel like WordPress does.
		dokuPass := paramOr(req.Params, "admin_password", "")
		if dokuPass == "" {
			dokuPass = ids.NewULID()
		}
		// Echo the chosen password back through the response so the
		// caller's reveal-once panel shows it; the upper-level
		// adminPassword variable is empty for RequiresDB=false apps
		// because we never went through provisionDBChain.
		adminPassword = dokuPass
		go createDokuWikiInstallAndKickAgent(ctx, dokuWikiKickArgs{
			InstallID:    installID,
			OSUser:       osUser,
			DocRoot:      domain.DocRoot,
			Subdirectory: install.Subdirectory,
			SiteURL:      siteURL,
			SiteTitle:    paramOr(req.Params, "site_title", "My DokuWiki"),
			AdminUser:    install.AdminUsername,
			AdminPass:    dokuPass,
			AdminEmail:   install.AdminEmail,
			License:      paramOr(req.Params, "license", "cc-by-sa"),
			UseWWW:       install.UseWWW,
		}, h.cfg)
	case "drupal":
		// Drupal mirrors MediaWiki's RequiresDB=true shape. Wiki admin
		// password and DB password are intentionally separate values:
		// the DB password is the machine-grade ULID from the chain;
		// admin_pass is what the user types (or we generate a memorable
		// fallback) for logging into Drupal admin.
		drupalPass := paramOr(req.Params, "admin_password", "")
		if drupalPass == "" {
			drupalPass = ids.NewULID()
		}
		go createDrupalInstallAndKickAgent(ctx, drupalKickArgs{
			InstallID:    installID,
			OSUser:       osUser,
			DocRoot:      domain.DocRoot,
			Subdirectory: install.Subdirectory,
			SiteURL:      siteURL,
			DBName:       chain.DBName,
			DBUser:       chain.DBUsername,
			DBPassword:   adminPassword, // DB password from the chain
			SiteTitle:    paramOr(req.Params, "site_title", "My Drupal Site"),
			AdminUser:    install.AdminUsername,
			AdminPass:    drupalPass,
			AdminEmail:   install.AdminEmail,
			SiteMail:     paramOr(req.Params, "site_mail", install.AdminEmail),
			Profile:      paramOr(req.Params, "profile", "standard"),
			UseWWW:       install.UseWWW,
		}, h.cfg)
		// Echo the admin password (not the DB one) back so the
		// reveal-once panel shows the credential the user needs to
		// log into Drupal admin.
		adminPassword = drupalPass
	case "joomla":
		// Joomla mirrors Drupal/MediaWiki: RequiresDB=true, separate
		// admin password from the DB password.
		joomlaPass := paramOr(req.Params, "admin_password", "")
		if joomlaPass == "" {
			joomlaPass = ids.NewULID()
		}
		go createJoomlaInstallAndKickAgent(ctx, joomlaKickArgs{
			InstallID:     installID,
			OSUser:        osUser,
			DocRoot:       domain.DocRoot,
			Subdirectory:  install.Subdirectory,
			SiteURL:       siteURL,
			DBName:        chain.DBName,
			DBUser:        chain.DBUsername,
			DBPassword:    adminPassword, // DB password from the chain
			SiteTitle:     paramOr(req.Params, "site_title", "My Joomla Site"),
			AdminUser:     install.AdminUsername,
			AdminPass:     joomlaPass,
			AdminEmail:    install.AdminEmail,
			AdminFullName: paramOr(req.Params, "admin_full_name", "Super User"),
			UseWWW:        install.UseWWW,
		}, h.cfg)
		adminPassword = joomlaPass
	case "phpbb":
		// phpBB mirrors Drupal/Joomla/MediaWiki shape.
		phpbbPass := paramOr(req.Params, "admin_password", "")
		if phpbbPass == "" {
			phpbbPass = ids.NewULID()
		}
		go createPhpBBInstallAndKickAgent(ctx, phpbbKickArgs{
			InstallID:        installID,
			OSUser:           osUser,
			DocRoot:          domain.DocRoot,
			Subdirectory:     install.Subdirectory,
			SiteURL:          siteURL,
			DBName:           chain.DBName,
			DBUser:           chain.DBUsername,
			DBPassword:       adminPassword,
			SiteTitle:        paramOr(req.Params, "site_title", "My Forum"),
			BoardDescription: paramOr(req.Params, "board_description", "A discussion forum"),
			AdminUser:        install.AdminUsername,
			AdminPass:        phpbbPass,
			AdminEmail:       install.AdminEmail,
			Language:         paramOr(req.Params, "language", "en"),
			UseWWW:           install.UseWWW,
		}, h.cfg)
		adminPassword = phpbbPass
	case "grav":
		// Grav is RequiresDB=false (flat-file, like DokuWiki), so no
		// chain.* values are populated. admin_password from params is
		// the only secret to surface.
		gravPass := paramOr(req.Params, "admin_password", "")
		if gravPass == "" {
			gravPass = ids.NewULID()
		}
		adminPassword = gravPass
		go createGravInstallAndKickAgent(ctx, gravKickArgs{
			InstallID:     installID,
			OSUser:        osUser,
			DocRoot:       domain.DocRoot,
			Subdirectory:  install.Subdirectory,
			SiteURL:       siteURL,
			SiteTitle:     paramOr(req.Params, "site_title", "My Grav Site"),
			AdminUser:     install.AdminUsername,
			AdminPass:     gravPass,
			AdminEmail:    install.AdminEmail,
			AdminFullName: paramOr(req.Params, "admin_full_name", "Site Administrator"),
			UseWWW:        install.UseWWW,
		}, h.cfg)
	case "matomo":
		matomoPass := paramOr(req.Params, "admin_password", "")
		if matomoPass == "" {
			matomoPass = ids.NewULID()
		}
		go createMatomoInstallAndKickAgent(ctx, matomoKickArgs{
			InstallID:    installID,
			OSUser:       osUser,
			DocRoot:      domain.DocRoot,
			Subdirectory: install.Subdirectory,
			SiteURL:      siteURL,
			DBName:       chain.DBName,
			DBUser:       chain.DBUsername,
			DBPassword:   adminPassword,
			AdminUser:    install.AdminUsername,
			AdminPass:    matomoPass,
			AdminEmail:   install.AdminEmail,
			UseWWW:       install.UseWWW,
		}, h.cfg)
		adminPassword = matomoPass
	case "glpi":
		glpiPass := paramOr(req.Params, "admin_password", "")
		if glpiPass == "" {
			glpiPass = ids.NewULID()
		}
		go createGLPIInstallAndKickAgent(ctx, glpiKickArgs{
			InstallID:    installID,
			OSUser:       osUser,
			DocRoot:      domain.DocRoot,
			Subdirectory: install.Subdirectory,
			SiteURL:      siteURL,
			DBName:       chain.DBName,
			DBUser:       chain.DBUsername,
			DBPassword:   adminPassword,
			AdminUser:    install.AdminUsername,
			AdminPass:    glpiPass,
			AdminEmail:   install.AdminEmail,
			Language:     paramOr(req.Params, "language", "en_GB"),
			UseWWW:       install.UseWWW,
		}, h.cfg)
		adminPassword = glpiPass
	case "moodle":
		// Moodle is the first consumer of the M19 managed-data-dir
		// framework — install_id is plumbed through to the agent so it
		// can ensure /home/<user>/<install_id>-data/ exists for
		// moodledata before kicking admin/cli/install.php.
		moodlePass := paramOr(req.Params, "admin_password", "")
		if moodlePass == "" {
			moodlePass = ids.NewULID()
		}
		go createMoodleInstallAndKickAgent(ctx, moodleKickArgs{
			InstallID:     installID,
			OSUser:        osUser,
			DocRoot:       domain.DocRoot,
			Subdirectory:  install.Subdirectory,
			SiteURL:       siteURL,
			DBName:        chain.DBName,
			DBUser:        chain.DBUsername,
			DBPassword:    adminPassword,
			SiteTitle:     paramOr(req.Params, "site_title", "My Moodle Site"),
			SiteShortName: paramOr(req.Params, "site_short_name", "Moodle"),
			AdminUser:     install.AdminUsername,
			AdminPass:     moodlePass,
			AdminEmail:    install.AdminEmail,
			Language:      paramOr(req.Params, "language", "en"),
			UseWWW:        install.UseWWW,
		}, h.cfg)
		adminPassword = moodlePass
	case "prestashop":
		prestaPass := paramOr(req.Params, "admin_password", "")
		if prestaPass == "" {
			prestaPass = ids.NewULID()
		}
		go createPrestaShopInstallAndKickAgent(ctx, prestashopKickArgs{
			InstallID:      installID,
			OSUser:         osUser,
			DocRoot:        domain.DocRoot,
			Subdirectory:   install.Subdirectory,
			SiteURL:        siteURL,
			DBName:         chain.DBName,
			DBUser:         chain.DBUsername,
			DBPassword:     adminPassword,
			SiteTitle:      paramOr(req.Params, "site_title", "My Shop"),
			AdminEmail:     install.AdminEmail,
			AdminPass:      prestaPass,
			AdminFirstName: paramOr(req.Params, "admin_first_name", "Site"),
			AdminLastName:  paramOr(req.Params, "admin_last_name", "Owner"),
			Country:        paramOr(req.Params, "country", "us"),
			Language:       paramOr(req.Params, "language", "en"),
			UseWWW:         install.UseWWW,
		}, h.cfg)
		adminPassword = prestaPass
	case "backdrop":
		backdropPass := paramOr(req.Params, "admin_password", "")
		if backdropPass == "" {
			backdropPass = ids.NewULID()
		}
		go createBackdropInstallAndKickAgent(ctx, backdropKickArgs{
			InstallID:    installID,
			OSUser:       osUser,
			DocRoot:      domain.DocRoot,
			Subdirectory: install.Subdirectory,
			SiteURL:      siteURL,
			DBName:       chain.DBName,
			DBUser:       chain.DBUsername,
			DBPassword:   adminPassword,
			SiteTitle:    paramOr(req.Params, "site_title", "My Backdrop Site"),
			AdminUser:    install.AdminUsername,
			AdminPass:    backdropPass,
			AdminEmail:   install.AdminEmail,
			Profile:      paramOr(req.Params, "profile", "standard"),
			UseWWW:       install.UseWWW,
		}, h.cfg)
		adminPassword = backdropPass
	case "abantecart":
		abantePass := paramOr(req.Params, "admin_password", "")
		if abantePass == "" {
			abantePass = ids.NewULID()
		}
		go createAbanteCartInstallAndKickAgent(ctx, abantecartKickArgs{
			InstallID:    installID,
			OSUser:       osUser,
			DocRoot:      domain.DocRoot,
			Subdirectory: install.Subdirectory,
			SiteURL:      siteURL,
			DBName:       chain.DBName,
			DBUser:       chain.DBUsername,
			DBPassword:   adminPassword,
			AdminUser:    install.AdminUsername,
			AdminPass:    abantePass,
			AdminEmail:   install.AdminEmail,
			UseWWW:       install.UseWWW,
		}, h.cfg)
		adminPassword = abantePass
	case "opencart":
		opencartPass := paramOr(req.Params, "admin_password", "")
		if opencartPass == "" {
			opencartPass = ids.NewULID()
		}
		go createOpenCartInstallAndKickAgent(ctx, opencartKickArgs{
			InstallID:    installID,
			OSUser:       osUser,
			DocRoot:      domain.DocRoot,
			Subdirectory: install.Subdirectory,
			SiteURL:      siteURL,
			DBName:       chain.DBName,
			DBUser:       chain.DBUsername,
			DBPassword:   adminPassword,
			AdminUser:    install.AdminUsername,
			AdminPass:    opencartPass,
			AdminEmail:   install.AdminEmail,
			UseWWW:       install.UseWWW,
		}, h.cfg)
		adminPassword = opencartPass
	case "concrete":
		concretePass := paramOr(req.Params, "admin_password", "")
		if concretePass == "" {
			concretePass = ids.NewULID()
		}
		go createConcreteInstallAndKickAgent(ctx, concreteKickArgs{
			InstallID:     installID,
			OSUser:        osUser,
			DocRoot:       domain.DocRoot,
			Subdirectory:  install.Subdirectory,
			SiteURL:       siteURL,
			DBName:        chain.DBName,
			DBUser:        chain.DBUsername,
			DBPassword:    adminPassword,
			SiteTitle:     paramOr(req.Params, "site_title", "My Concrete Site"),
			AdminUser:     install.AdminUsername,
			AdminPass:     concretePass,
			AdminEmail:    install.AdminEmail,
			StartingPoint: paramOr(req.Params, "starting_point", "elemental_full"),
			Locale:        paramOr(req.Params, "locale", "en_US"),
			UseWWW:        install.UseWWW,
		}, h.cfg)
		adminPassword = concretePass
	case "freshrss":
		// FreshRSS mirrors the generic RequiresDB=true shape; password
		// from params or generated, separate from the DB password.
		freshrssPass := paramOr(req.Params, "admin_password", "")
		if freshrssPass == "" {
			freshrssPass = ids.NewULID()
		}
		go createFreshRSSInstallAndKickAgent(ctx, freshrssKickArgs{
			InstallID:    installID,
			OSUser:       osUser,
			DocRoot:      domain.DocRoot,
			Subdirectory: install.Subdirectory,
			SiteURL:      siteURL,
			DBName:       chain.DBName,
			DBUser:       chain.DBUsername,
			DBPassword:   adminPassword,
			AdminUser:    install.AdminUsername,
			AdminPass:    freshrssPass,
			AdminEmail:   install.AdminEmail,
			Language:     paramOr(req.Params, "language", "en"),
			UseWWW:       install.UseWWW,
		}, h.cfg)
		adminPassword = freshrssPass
	case "mediawiki":
		// MediaWiki requires a database (RequiresDB=true → adminPassword
		// already populated by provisionDBChain via the DB-user path).
		// admin_password the user typed/generated for the wiki admin
		// account is a separate value; we don't reuse the DB password
		// because that one's machine-grade ULID — fine for DB but the
		// user wanted to type a memorable wiki admin password.
		mwPass := paramOr(req.Params, "admin_password", "")
		if mwPass == "" {
			mwPass = ids.NewULID()
		}
		// MediaWiki minimum admin password length is 10. ULID is 26
		// chars so the generator path always satisfies; a user-supplied
		// shorter password would be rejected by the agent's validator.
		go createMediaWikiInstallAndKickAgent(ctx, mediaWikiKickArgs{
			InstallID:    installID,
			OSUser:       osUser,
			DocRoot:      domain.DocRoot,
			Subdirectory: install.Subdirectory,
			SiteURL:      siteURL,
			DBName:       chain.DBName,
			DBUser:       chain.DBUsername,
			DBPassword:   adminPassword, // DB password from the chain; reused as installdbpass
			SiteTitle:    paramOr(req.Params, "site_title", "My MediaWiki"),
			AdminUser:    paramOr(req.Params, "admin_username", "Admin"),
			AdminPass:    mwPass,
			AdminEmail:   install.AdminEmail,
			Language:     paramOr(req.Params, "language", "en"),
			UseWWW:       install.UseWWW,
		}, h.cfg)
		// Echo the wiki admin password (NOT the DB one) back through
		// the response so the reveal-once panel shows the credential
		// the user actually needs to log in to the wiki.
		adminPassword = mwPass
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
