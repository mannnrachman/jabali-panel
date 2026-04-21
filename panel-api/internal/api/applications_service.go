package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// InstallApplication is the transport-agnostic version of the gin
// create handler. It exists so the CLI (panel-api/cmd/server/app_cmd.go)
// can drive the same install pipeline as HTTP without going through
// /api/v1/applications — over the HTTP API, an in-panel auth middleware (RequireKratosSession)
// are 401'd at the HTTP edge, but the service-level pipeline is the
// same.
//
// Returns one of (*InstallResult, nil) or (nil, *InstallError). HTTP
// callers map InstallError.HTTPStatus → response code; CLI callers map
// it to a terminal error.

// InstallParams is the input shape. Handler/CLI assemble these from
// their respective contexts (claims vs operator-supplied flags).
type InstallParams struct {
	AppType      string
	UserID       string // owner for the install (HTTP: claims.UserID; CLI: domain.UserID)
	IsAdminCall  bool   // when true, ownership mismatch returns 403 instead of 404 — preserves the HTTP handler's "don't leak existence" behaviour for non-admins
	DomainID     string
	Subdirectory string
	UseWWW       bool
	Params       map[string]any
}

// InstallResult mirrors the gin response shape — Install carries the
// row that was inserted, AdminPassword is the plaintext credential
// surfaced once (the row stores no password, only the username/email).
type InstallResult struct {
	Install       *models.ApplicationInstall
	AdminPassword string
}

// InstallError is the strongly-typed failure mode. Code matches the
// JSON `error` field the HTTP handler used pre-extraction so existing
// clients see the same payload. Detail is optional.
type InstallError struct {
	Code       string
	Detail     string
	HTTPStatus int
}

func (e *InstallError) Error() string {
	if e.Detail != "" {
		return e.Code + ": " + e.Detail
	}
	return e.Code
}

func newInstallErr(status int, code, detail string) *InstallError {
	return &InstallError{Code: code, Detail: detail, HTTPStatus: status}
}

// InstallApplication runs the full create pipeline:
//
//  1. Resolve descriptor + validate subdirectory + validate params
//  2. Load domain + ownership check
//  3. Reject if (domain, subdirectory) slot is already taken
//  4. Resolve owner's linux username
//  5. Provision DB chain (if descriptor.RequiresDB)
//  6. Generate admin username (server-side, never trusted from caller)
//  7. Insert install row (status=pending)
//  8. Dispatch the per-app kicker goroutine
//
// Side-effects (DB rows created, agent calls fired) are identical to
// the previous gin handler. The body is deliberately a near-1:1 move
// — diffing it against the old handler should show only `c.JSON(...);
// return` → `return nil, newInstallErr(...)` translations.
func InstallApplication(ctx context.Context, deps ApplicationHandlerConfig, p InstallParams) (*InstallResult, *InstallError) {
	if deps.Apps == nil {
		// Wiring bug, not user error — fail loud so it shows up in
		// dev/test. In production this is impossible because
		// app.NewWithDeps registers the panic-on-nil at startup.
		return nil, newInstallErr(http.StatusInternalServerError, "internal", "apps registry not wired")
	}

	descriptor, ok := deps.Apps.Get(p.AppType)
	if !ok {
		return nil, newInstallErr(http.StatusBadRequest, "invalid_app_type", "unknown app: "+p.AppType)
	}

	if err := validateSubdirectory(p.Subdirectory); err != nil {
		return nil, newInstallErr(http.StatusBadRequest, "invalid_subdirectory", err.Error())
	}

	if err := validateInstallParams(p.Params, descriptor.InstallParamSchema); err != nil {
		return nil, newInstallErr(http.StatusBadRequest, "invalid_params", err.Error())
	}

	domain, err := deps.Domains.FindByID(ctx, p.DomainID)
	if err != nil {
		if isNotFound(err) {
			return nil, newInstallErr(http.StatusNotFound, "domain_not_found", "")
		}
		slog.ErrorContext(ctx, "applications create: domain lookup failed", "err", err, "domain_id", p.DomainID)
		return nil, newInstallErr(http.StatusInternalServerError, "internal", "")
	}
	if domain.UserID != p.UserID {
		// Mirror the HTTP handler's "don't leak existence" rule: an
		// admin acting on someone else's domain gets 403; a non-admin
		// gets 404 so they can't confirm the row exists.
		if p.IsAdminCall {
			return nil, newInstallErr(http.StatusForbidden, "forbidden", "")
		}
		return nil, newInstallErr(http.StatusNotFound, "domain_not_found", "")
	}

	// (domain, subdirectory) precheck — at most one app per slot
	// regardless of app_type. Migration 000046 still includes app_type
	// in the DB UNIQUE for forward compat, so this stricter rule lives
	// at the API boundary.
	existing, lookupErr := deps.ApplicationInstalls.FindByDomainAndSubdirectory(ctx, p.DomainID, p.Subdirectory)
	if lookupErr == nil && existing != nil {
		return nil, newInstallErr(http.StatusConflict, "install_exists", "")
	}
	if lookupErr != nil && !isNotFound(lookupErr) {
		slog.ErrorContext(ctx, "applications create: existing install lookup failed", "err", lookupErr)
		return nil, newInstallErr(http.StatusInternalServerError, "internal", "")
	}

	var osUser string
	if u, uErr := deps.Users.FindByID(ctx, p.UserID); uErr == nil && u != nil && u.Username != nil {
		osUser = *u.Username
	}
	if osUser == "" {
		slog.ErrorContext(ctx, "applications create: user has no linux username", "user_id", p.UserID)
		return nil, newInstallErr(http.StatusConflict, "user_not_provisioned", "")
	}

	now := time.Now().UTC()

	// Optional DB chain. RequiresDB=false apps (DokuWiki, Grav)
	// continue with chain zero-value so DBID="" and the install row
	// records no database.
	var (
		chain         provisionedDB
		adminPassword string
	)
	if descriptor.RequiresDB {
		adminPassword, _ = paramString(p.Params, "admin_password")
		if adminPassword == "" {
			adminPassword = ids.NewULID()
		}
		chain, err = provisionDBChain(ctx, deps, p.UserID, osUser, adminPassword)
		if err != nil {
			slog.ErrorContext(ctx, "applications create: provision db chain", "err", err)
			return nil, newInstallErr(http.StatusBadGateway, "agent_failed", err.Error())
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
		UserID:        p.UserID,
		DomainID:      p.DomainID,
		DBID:          models.DBIDPtr(chain.DBID),
		AppType:       descriptor.Name,
		AdminUsername: adminUsername,
		AdminEmail:    paramOr(p.Params, "admin_email", ""),
		Locale:        paramOr(p.Params, "locale", "en_US"),
		UseWWW:        p.UseWWW,
		Subdirectory:  p.Subdirectory,
		Status:        "pending",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := deps.ApplicationInstalls.Create(ctx, install); err != nil {
		if descriptor.RequiresDB {
			rollbackDBChain(ctx, deps, chain)
		}
		slog.ErrorContext(ctx, "applications create: install row create failed", "err", err, "install_id", installID)
		return nil, newInstallErr(http.StatusInternalServerError, "internal", "")
	}

	// Per-app install kicker. Adding an app means adding a case here +
	// the descriptor + the agent installer. Each branch may also
	// overwrite adminPassword with the per-app credential the user
	// will actually use to log in (separate from the DB password).
	siteURL := buildSiteURL(domain.Name, p.UseWWW, p.Subdirectory)

	// Snapshot the install state before launching the async kicker.
	// The kicker goroutine calls UpdateStatus, which the GORM repo
	// implements as SQL UPDATE (no shared memory), but in-memory repos
	// (tests, potential future caches) may mutate the *ApplicationInstall
	// pointer we also return to the HTTP handler — a data race under
	// `go test -race`. The HTTP response should reflect the deterministic
	// "pending" state at creation time, not whatever the kicker raced to
	// write first; snapshotting gives callers a stable view either way.
	snapshot := *install

	adminPassword = dispatchInstallKicker(ctx, descriptor.Name, kickContext{
		InstallID:        installID,
		OSUser:           osUser,
		DocRoot:          domain.DocRoot,
		Subdirectory:     install.Subdirectory,
		SiteURL:          siteURL,
		AdminUsername:    install.AdminUsername,
		AdminEmail:       install.AdminEmail,
		Locale:           install.Locale,
		UseWWW:           install.UseWWW,
		Chain:            chain,
		Params:           p.Params,
		DBPassword:       adminPassword,
	}, deps)

	return &InstallResult{Install: &snapshot, AdminPassword: adminPassword}, nil
}

// kickContext bundles the per-app args we already computed so the
// dispatcher doesn't re-derive them in every case. DBPassword is
// pre-populated from the chain (or empty for RequiresDB=false apps);
// the per-app kicker may either reuse it (WordPress) or generate its
// own admin password (Drupal/Joomla/...) and return it as the new
// AdminPassword to surface.
type kickContext struct {
	InstallID     string
	OSUser        string
	DocRoot       string
	Subdirectory  string
	SiteURL       string
	AdminUsername string
	AdminEmail    string
	Locale        string
	UseWWW        bool
	Chain         provisionedDB
	Params        map[string]any
	DBPassword    string
}

// dispatchInstallKicker runs the per-app installer goroutine and
// returns the admin password to surface in the response. The body is
// the same per-app switch the original gin handler had — extracted so
// both InstallApplication (service) and CLI can call it.
func dispatchInstallKicker(ctx context.Context, appName string, k kickContext, deps ApplicationHandlerConfig) string {
	adminPassword := k.DBPassword
	switch appName {
	case "wordpress":
		go createInstallAndKickAgent(ctx, installKickArgs{
			InstallID:        k.InstallID,
			OSUser:           k.OSUser,
			DocRoot:          k.DocRoot,
			DBName:           k.Chain.DBName,
			DBUser:           k.Chain.DBUsername,
			DBPassword:       adminPassword,
			SiteURL:          k.SiteURL,
			SiteTitle:        paramOr(k.Params, "site_title", "My WordPress Site"),
			AdminUsername:    k.AdminUsername,
			AdminPassword:    adminPassword,
			AdminEmail:       k.AdminEmail,
			Locale:           k.Locale,
			Subdirectory:     k.Subdirectory,
			UseWWW:           k.UseWWW,
		}, deps)
	case "drupal":
		drupalPass := paramOr(k.Params, "admin_password", "")
		if drupalPass == "" {
			drupalPass = ids.NewULID()
		}
		go createDrupalInstallAndKickAgent(ctx, drupalKickArgs{
			InstallID:    k.InstallID,
			OSUser:       k.OSUser,
			DocRoot:      k.DocRoot,
			Subdirectory: k.Subdirectory,
			SiteURL:      k.SiteURL,
			DBName:       k.Chain.DBName,
			DBUser:       k.Chain.DBUsername,
			DBPassword:   adminPassword,
			SiteTitle:    paramOr(k.Params, "site_title", "My Drupal Site"),
			AdminUser:    k.AdminUsername,
			AdminPass:    drupalPass,
			AdminEmail:   k.AdminEmail,
			SiteMail:     paramOr(k.Params, "site_mail", k.AdminEmail),
			Profile:      paramOr(k.Params, "profile", "standard"),
			UseWWW:       k.UseWWW,
		}, deps)
		adminPassword = drupalPass
	case "joomla":
		joomlaPass := paramOr(k.Params, "admin_password", "")
		if joomlaPass == "" {
			joomlaPass = ids.NewULID()
		}
		go createJoomlaInstallAndKickAgent(ctx, joomlaKickArgs{
			InstallID:     k.InstallID,
			OSUser:        k.OSUser,
			DocRoot:       k.DocRoot,
			Subdirectory:  k.Subdirectory,
			SiteURL:       k.SiteURL,
			DBName:        k.Chain.DBName,
			DBUser:        k.Chain.DBUsername,
			DBPassword:    adminPassword,
			SiteTitle:     paramOr(k.Params, "site_title", "My Joomla Site"),
			AdminUser:     k.AdminUsername,
			AdminPass:     joomlaPass,
			AdminEmail:    k.AdminEmail,
			AdminFullName: paramOr(k.Params, "admin_full_name", "Super User"),
			UseWWW:        k.UseWWW,
		}, deps)
		adminPassword = joomlaPass
	case "phpbb":
		phpbbPass := paramOr(k.Params, "admin_password", "")
		if phpbbPass == "" {
			phpbbPass = ids.NewULID()
		}
		go createPhpBBInstallAndKickAgent(ctx, phpbbKickArgs{
			InstallID:        k.InstallID,
			OSUser:           k.OSUser,
			DocRoot:          k.DocRoot,
			Subdirectory:     k.Subdirectory,
			SiteURL:          k.SiteURL,
			DBName:           k.Chain.DBName,
			DBUser:           k.Chain.DBUsername,
			DBPassword:       adminPassword,
			SiteTitle:        paramOr(k.Params, "site_title", "My Forum"),
			BoardDescription: paramOr(k.Params, "board_description", "A discussion forum"),
			AdminUser:        k.AdminUsername,
			AdminPass:        phpbbPass,
			AdminEmail:       k.AdminEmail,
			Language:         paramOr(k.Params, "language", "en"),
			UseWWW:           k.UseWWW,
		}, deps)
		adminPassword = phpbbPass
	case "prestashop":
		prestaPass := paramOr(k.Params, "admin_password", "")
		if prestaPass == "" {
			prestaPass = ids.NewULID()
		}
		go createPrestaShopInstallAndKickAgent(ctx, prestashopKickArgs{
			InstallID:      k.InstallID,
			OSUser:         k.OSUser,
			DocRoot:        k.DocRoot,
			Subdirectory:   k.Subdirectory,
			SiteURL:        k.SiteURL,
			DBName:         k.Chain.DBName,
			DBUser:         k.Chain.DBUsername,
			DBPassword:     adminPassword,
			SiteTitle:      paramOr(k.Params, "site_title", "My Shop"),
			AdminEmail:     k.AdminEmail,
			AdminPass:      prestaPass,
			AdminFirstName: paramOr(k.Params, "admin_first_name", "Site"),
			AdminLastName:  paramOr(k.Params, "admin_last_name", "Owner"),
			Country:        paramOr(k.Params, "country", "us"),
			Language:       paramOr(k.Params, "language", "en"),
			UseWWW:         k.UseWWW,
		}, deps)
		adminPassword = prestaPass
	case "opencart":
		opencartPass := paramOr(k.Params, "admin_password", "")
		if opencartPass == "" {
			opencartPass = ids.NewULID()
		}
		go createOpenCartInstallAndKickAgent(ctx, opencartKickArgs{
			InstallID:    k.InstallID,
			OSUser:       k.OSUser,
			DocRoot:      k.DocRoot,
			Subdirectory: k.Subdirectory,
			SiteURL:      k.SiteURL,
			DBName:       k.Chain.DBName,
			DBUser:       k.Chain.DBUsername,
			DBPassword:   adminPassword,
			AdminUser:    k.AdminUsername,
			AdminPass:    opencartPass,
			AdminEmail:   k.AdminEmail,
			UseWWW:       k.UseWWW,
		}, deps)
		adminPassword = opencartPass
	case "mediawiki":
		mwPass := paramOr(k.Params, "admin_password", "")
		if mwPass == "" {
			mwPass = ids.NewULID()
		}
		go createMediaWikiInstallAndKickAgent(ctx, mediaWikiKickArgs{
			InstallID:    k.InstallID,
			OSUser:       k.OSUser,
			DocRoot:      k.DocRoot,
			Subdirectory: k.Subdirectory,
			SiteURL:      k.SiteURL,
			DBName:       k.Chain.DBName,
			DBUser:       k.Chain.DBUsername,
			DBPassword:   adminPassword,
			SiteTitle:    paramOr(k.Params, "site_title", "My MediaWiki"),
			AdminUser:    paramOr(k.Params, "admin_username", "Admin"),
			AdminPass:    mwPass,
			AdminEmail:   k.AdminEmail,
			Language:     paramOr(k.Params, "language", "en"),
			UseWWW:       k.UseWWW,
		}, deps)
		adminPassword = mwPass
	}
	return adminPassword
}
