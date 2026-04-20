// Package app wires HTTP routes, middleware and lifecycle together.
// Downstream packages (handlers, middleware, repositories) plug in here.
package app

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/apps"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/hydraclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/reconciler"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/sso"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/webui"
	panelui "git.linux-hosting.co.il/shukivaknin/jabali2/panel-ui"
)

// Deps bundles the collaborators NewWithDeps needs so main.go keeps its
// argument list short. Anything a handler family needs — Kratos client,
// repositories — plugs in here.
type Deps struct {
	KratosClient        *kratosclient.Client
	// HydraClient is the admin-API client for Ory Hydra (OAuth 2 /
	// OIDC). Nil in environments without install_hydra (dev, pre-M16).
	// When nil, OAuth2 flow routes are not registered and the
	// applications framework's OIDC client provisioning is skipped.
	HydraClient         *hydraclient.Client
	Users               repository.UserRepository
	Packages            repository.PackageRepository
	Domains             repository.DomainRepository
	Databases           repository.DatabaseRepository
	DatabaseUsers       repository.DatabaseUserRepository
	DatabaseUserGrants  repository.DatabaseUserGrantRepository
	PhpMyAdminSSOTokens repository.PhpMyAdminSSOTokenRepository
	Agent               agent.AgentInterface
	Reconciler          *reconciler.Reconciler
	ServerSettings      repository.ServerSettingsRepository
	DNSZones            repository.DNSZoneRepository
	DNSRecords          repository.DNSRecordRepository
	SSLCerts            repository.SSLCertificateRepository
	PHPPools            repository.PHPPoolRepository
	PHPPoolIniOverrides repository.PHPPoolIniOverrideRepository
	WordPressInstalls   repository.WordPressInstallRepository
	// Apps is the M19 application registry — descriptors for every
	// installable app (WordPress, future DokuWiki, etc.). Step 3 will
	// hand this to the generic /applications handlers. NewWithDeps
	// builds a default-populated registry when this is nil so the
	// existing test wiring (`Deps{}`) keeps working without each
	// caller knowing about the registry.
	Apps                *apps.Registry
	CronJobs            repository.CronJobRepository
	SSHKeys             repository.SSHKeyRepository
	LimitOverrides      repository.UserLimitOverrideRepository
	// QuotaMount is the filesystem mount path /home lives on — passed
	// on every M18 user.limits.{apply,clear,report} agent call so the
	// agent can resolve `setquota -u <user> ... <mount>` without ever
	// defaulting to the disruptive `-a` flag. Resolved at startup via
	// internal/limits.QuotaMountFor("/home"); empty string disables
	// the disk-quota half of the limits pipeline (dev boxes).
	QuotaMount          string
	SSO                 *sso.Service
	SSOKey              *ssokey.Key
	Log                 *slog.Logger
}

// Default tier: chosen so a reasonable SPA (polling, a few concurrent
// tabs) never notices, but a misbehaving client does. Tunable later via
// config if we hit real-world ceilings.
const (
	rateDefaultPerSec = 10.0
	rateDefaultBurst  = 40

	// Strict tier: credential endpoints. 5 req/min with a burst of 5
	// permits a typo-and-retry session without opening the door to
	// brute force from a single IP.
	rateStrictPerSec = 5.0 / 60.0
	rateStrictBurst  = 5

	rateLimiterIdleCleanup = 10 * time.Minute
	rateLimiterSweepEvery  = 5 * time.Minute
)

// New returns a Gin engine configured with development defaults — no auth,
// no DB. Used by tests and any caller that doesn't need downstream deps.
// Production callers should use NewWithDeps so auth routes are mounted.
func New() *gin.Engine {
	return NewWithDeps(config.Defaults(), Deps{})
}

// NewWithDeps is the canonical constructor for the server. Handlers that
// depend on external collaborators (auth service, JWT issuer) are mounted
// only when their dep is non-nil, so early-phase deployments can still boot
// with a partial Deps.
func NewWithDeps(cfg *config.Config, deps Deps) *gin.Engine {
	if cfg.Server.Env == config.EnvProduction {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	// Apps registry. Built once per server. RegisterDefaults panics on
	// programmer error (duplicate name, bad ParamSpec) — those are
	// startup conditions we want surfaced loudly, not eaten by a
	// silent boot.
	if deps.Apps == nil {
		deps.Apps = apps.New()
		if err := apps.RegisterDefaults(deps.Apps); err != nil {
			panic("apps.RegisterDefaults: " + err.Error())
		}
	}

	r := gin.New()
	r.HandleMethodNotAllowed = true

	// Global middleware. Order matters:
	//   1. Recovery so a panic never leaves a connection open.
	//   2. RequestID so all subsequent log lines (including panics) carry it.
	//   3. CORS so preflights short-circuit before rate limit consumes tokens.
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.CORS(cfg.CORS.AllowedOrigins))

	// Rate limiter — shared across handler mounts so both tiers count against
	// the same per-IP state. A background goroutine reaps idle buckets.
	rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		DefaultRate:  rate.Limit(rateDefaultPerSec),
		DefaultBurst: rateDefaultBurst,
		StrictRate:   rate.Limit(rateStrictPerSec),
		StrictBurst:  rateStrictBurst,
	})
	startRateLimiterSweeper(rl)
	r.Use(rl.Default())

	// Tag every /api/v1/* response with Cache-Control: no-store. Without
	// this, Firefox's Opaque-Response-Blocking decision cache can replay
	// a prior 401 with status=0ms, leaving the SPA stuck on a blank page
	// after token expiry.
	r.Use(middleware.NoCacheAPI())

	api.RegisterServiceInfoRoute(r)
	api.RegisterHealthRoutes(r)
	if deps.Agent != nil {
		api.RegisterAgentHealthRoute(r, deps.Agent)
	}

	// Instantiate Kratos client + same-origin reverse proxy for /.ory/*.
	// The SPA fetches relative Kratos self-service endpoints and panel-api
	// binds :8443 directly (no nginx in front on that port), so we proxy
	// in-process. See kratos_proxy.go for the full rationale.
	if cfg.Auth.Kratos.PublicURL != "" {
		deps.KratosClient = kratosclient.NewClient(cfg.Auth.Kratos.PublicURL, cfg.Auth.Kratos.AdminURL)

		if err := RegisterKratosProxy(r, cfg.Auth.Kratos.PublicURL); err != nil {
			deps.Log.Error("registering Kratos reverse proxy failed; login will be broken",
				"err", err, "public_url", cfg.Auth.Kratos.PublicURL)
		}
	}

	// Same-origin reverse proxy for Hydra's public OAuth 2 / OIDC
	// endpoints. See hydra_proxy.go for the full route list and why
	// the admin API is deliberately NOT proxied.
	//
	// Hydra is optional: environments without install_hydra() (dev, or
	// installs predating M16) leave HydraConfig empty and /oauth2/*
	// routes stay unrouted. The applications framework's OIDC client
	// provisioning (Step 6) will no-op in that case.
	if cfg.Auth.Hydra.PublicURL != "" {
		if err := RegisterHydraProxy(r, cfg.Auth.Hydra.PublicURL); err != nil {
			deps.Log.Error("registering Hydra reverse proxy failed; OIDC SSO will be broken",
				"err", err, "public_url", cfg.Auth.Hydra.PublicURL)
		}
	}

	// Hydra admin client — used by oauth2_flow handlers and (Wave D)
	// apps framework client-provisioning. Only constructed when the
	// admin URL is configured; pre-M16 installs leave this nil and
	// the OAuth2 routes skip registration below.
	if cfg.Auth.Hydra.AdminURL != "" && deps.HydraClient == nil {
		deps.HydraClient = hydraclient.New(cfg.Auth.Hydra.AdminURL)
	}

	if deps.KratosClient != nil {
		// Protected API group — everything under /api/v1/* flows through
		// RequireKratosSession, which resolves Kratos identity UUIDs →
		// panel user rows so claims.UserID carries the ULID every
		// ownership check in the API expects.
		authMiddleware := middleware.RequireKratosSession(deps.KratosClient, deps.Users)
		v1 := r.Group("/api/v1", authMiddleware)
		api.RegisterMeRoutes(v1, api.MeHandlerConfig{
			Users:          deps.Users,
			ServerSettings: deps.ServerSettings,
		})

		// OAuth2 flow handlers (login-start, consent-start, accept,
		// deny, consent metadata). Only registered when Hydra is
		// configured — pre-M16 installs skip this and /oauth2-login
		// falls through to the SPA's NoRoute fallback (which will
		// 404 via webui's API-prefix guard).
		if deps.HydraClient != nil {
			api.RegisterOAuth2FlowRoutes(v1, r, api.OAuth2FlowHandlerConfig{
				Hydra: deps.HydraClient,
				Log:   deps.Log,
			})
		}

		if deps.Users != nil {
			api.RegisterUserRoutes(v1, api.UserHandlerConfig{
				Repo:            deps.Users,
				Agent:           deps.Agent,
				StrictRateLimit: rl.Strict(),
				Domains:         deps.Domains,
				Reconciler:      deps.Reconciler,
				Log:             deps.Log,
				// M20: atomic Kratos identity creation on POST /users.
				KratosClient: deps.KratosClient,
			})
		}
		if deps.Packages != nil {
			api.RegisterPackageRoutes(v1, api.PackageHandlerConfig{Repo: deps.Packages})
		}
		// M18 user-limits endpoints. Mount only when all deps are
		// present — a pre-M18 deployment with no LimitOverrides repo
		// simply won't expose the endpoints.
		if deps.Users != nil && deps.Packages != nil && deps.LimitOverrides != nil {
			api.RegisterUserLimitsRoutes(v1, api.UserLimitsHandlerConfig{
				Users:          deps.Users,
				Packages:       deps.Packages,
				LimitOverrides: deps.LimitOverrides,
				Agent:          deps.Agent,
				QuotaMount:     deps.QuotaMount,
			})
		}
		if deps.Domains != nil {
			api.RegisterDomainRoutes(v1, api.DomainHandlerConfig{
				Domains:    deps.Domains,
				Users:      deps.Users,
				Packages:   deps.Packages,
				Agent:      deps.Agent,
				Reconciler: deps.Reconciler,
				SSLCerts:   deps.SSLCerts,
			})
		}
		if deps.Databases != nil && deps.DatabaseUsers != nil {
			api.RegisterDatabaseRoutes(v1, api.DatabaseHandlerConfig{
				Databases:         deps.Databases,
				DatabaseUsers:     deps.DatabaseUsers,
				DatabaseGrants:    deps.DatabaseUserGrants,
				WordPressInstalls: deps.WordPressInstalls,
				Users:             deps.Users,
				Packages:          deps.Packages,
				Agent:             deps.Agent,
			})
		}
		if deps.DatabaseUsers != nil && deps.DatabaseUserGrants != nil {
			api.RegisterDatabaseUserRoutes(v1, api.DatabaseUserHandlerConfig{
				Databases:      deps.Databases,
				DatabaseUsers:  deps.DatabaseUsers,
				DatabaseGrants: deps.DatabaseUserGrants,
				Users:          deps.Users,
				Packages:       deps.Packages,
				Agent:          deps.Agent,
			})
		}
		if deps.Domains != nil && deps.DNSZones != nil && deps.DNSRecords != nil {
			api.RegisterDNSRoutes(v1, api.DNSHandlerConfig{
				Domains:    deps.Domains,
				Zones:      deps.DNSZones,
				Records:    deps.DNSRecords,
				Reconciler: deps.Reconciler,
			})
		}
		if deps.Domains != nil && deps.SSLCerts != nil {
			api.RegisterSSLRoutes(v1, api.SSLHandlerConfig{
				Domains:        deps.Domains,
				SSLCerts:       deps.SSLCerts,
				ServerSettings: deps.ServerSettings,
				Reconciler:     deps.Reconciler,
				Config:         cfg,
			})
		}
		if deps.ServerSettings != nil {
			api.RegisterServerSettingsRoutes(v1, api.ServerSettingsHandlerConfig{
				Repo:  deps.ServerSettings,
				Agent: deps.Agent,
				Log:   deps.Log,
			})
		}
		if deps.Agent != nil {
			api.RegisterSystemRoutes(v1, deps.Agent)
			api.RegisterPHPVersionRoutes(v1, deps.Agent)
		}
		if deps.SSO != nil && deps.Databases != nil && deps.PhpMyAdminSSOTokens != nil {
			api.RegisterSSOPhpMyAdminRoutes(v1, api.SSOPhpMyAdminHandlerConfig{
				Databases: deps.Databases,
				SSO:       deps.SSO,
				Log:       deps.Log,
			})
		}
		if deps.PHPPools != nil && deps.PHPPoolIniOverrides != nil {
			api.RegisterPHPPoolRoutes(v1, api.PHPPoolHandlerConfig{
				PHPPools:            deps.PHPPools,
				PHPPoolIniOverrides: deps.PHPPoolIniOverrides,
				Domains:             deps.Domains,
				Users:               deps.Users,
				Agent:               deps.Agent,
			})
		}
		if deps.Domains != nil && deps.PHPPools != nil {
			api.RegisterDomainPHPPoolRoutes(v1, api.DomainPHPPoolHandlerConfig{
				Domains:             deps.Domains,
				PHPPools:            deps.PHPPools,
				PHPPoolIniOverrides: deps.PHPPoolIniOverrides,
				Users:               deps.Users,
				Agent:               deps.Agent,
			})
		}
		if deps.Domains != nil {
			api.RegisterDomainPHPSettingsRoutes(v1, api.DomainPHPSettingsHandlerConfig{
				Domains:  deps.Domains,
				PHPPools: deps.PHPPools,
			})
		}
		if deps.WordPressInstalls != nil && deps.Databases != nil && deps.DatabaseUsers != nil &&
			deps.DatabaseUserGrants != nil && deps.Domains != nil && deps.Users != nil && deps.Agent != nil {
			appCfg := api.ApplicationHandlerConfig{
				ApplicationInstalls: deps.WordPressInstalls,
				Databases:           deps.Databases,
				DatabaseUsers:       deps.DatabaseUsers,
				DatabaseGrants:      deps.DatabaseUserGrants,
				Domains:             deps.Domains,
				Users:               deps.Users,
				Packages:            deps.Packages,
				Agent:               deps.Agent,
				Apps:                deps.Apps,
				// M16 Wave D: OIDC client provisioning on app install.
				// Nil-safe: when either HydraClient or SSOKey is missing,
				// InstallApplication skips minting and the install
				// proceeds without per-install SSO.
				HydraClient: deps.HydraClient,
				SSOKey:      deps.SSOKey,
				// PanelBaseURL is the public HTTPS URL of the panel
				// itself — what Hydra advertises as its OIDC issuer.
				// Consumed by Step 7 (the WordPress plugin needs it to
				// fetch /.well-known/openid-configuration). Not yet
				// surfaced in config.toml; wire to an explicit field
				// in a follow-up alongside the plugin auto-install.
				PanelBaseURL: "",
			}
			api.RegisterWordPressRoutes(v1, appCfg)
			api.RegisterApplicationRoutes(v1, appCfg)
		}

		// Cron jobs routes (M8)
		if deps.CronJobs != nil && deps.Users != nil && deps.Domains != nil && deps.Agent != nil {
			api.RegisterCronRoutes(v1, api.CronHandlerConfig{
				CronJobs: deps.CronJobs,
				Users:    deps.Users,
				Domains:  deps.Domains,
				Agent:    deps.Agent,
				Log:      deps.Log,
			})
		}

		// File manager routes (M11)
		if deps.Users != nil && deps.Agent != nil {
			api.RegisterFilesRoutes(v1, api.FilesHandlerConfig{
				Users:   deps.Users,
				Domains: deps.Domains,
				Agent:   deps.Agent,
				Log:     deps.Log,
			})
		}

		// SSH keys routes
		if deps.SSHKeys != nil && deps.Reconciler != nil {
			api.RegisterSSHKeysRoutes(v1, api.SSHKeysHandlerConfig{
				SSHKeys:    deps.SSHKeys,
				Reconciler: deps.Reconciler,
				Logger:     deps.Log,
			})
		}

		// Admin routes
		if deps.Reconciler != nil {
			admin := v1.Group("/admin", middleware.RequireAdmin())
			api.RegisterReconcileRoutes(admin, &api.ReconcileHandlerConfig{
				Reconciler: deps.Reconciler,
				Log:        deps.Log,
			})
		}
		if deps.Agent != nil {
			admin := v1.Group("/admin", middleware.RequireAdmin())
			api.RegisterPHPVersionAdminRoutes(admin, deps.Agent, deps.ServerSettings)
			api.RegisterPHPExtensionAdminRoutes(admin, deps.Agent)
		}
	}

	// Static SPA: owns r.NoRoute — serves embedded panel-ui/dist for
	// unknown paths with client-side routing fallback, JSON 404 for
	// API-ish prefixes.
	webui.RegisterStatic(r, panelui.Assets())
	// Method-not-allowed handler stays on the api side for JSON symmetry.
	api.RegisterMethodNotAllowedHandler(r)
	return r
}

// startRateLimiterSweeper launches a background goroutine that drops idle
// per-IP buckets so the limiter's memory stays bounded. Safe to call once
// per process; the goroutine lives for the program's lifetime.
func startRateLimiterSweeper(rl *middleware.RateLimiter) {
	ticker := time.NewTicker(rateLimiterSweepEvery)
	go func() {
		for range ticker.C {
			rl.Cleanup(rateLimiterIdleCleanup)
		}
	}()
}
