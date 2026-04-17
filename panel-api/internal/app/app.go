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
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/reconciler"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/webui"
	panelui "git.linux-hosting.co.il/shukivaknin/jabali2/panel-ui"
)

// Deps bundles the collaborators NewWithDeps needs so main.go keeps its
// argument list short. Anything a handler family needs — auth service, JWT
// issuer, repositories — plugs in here.
type Deps struct {
	Auth                api.AuthService
	JWTIssuer           *auth.JWTIssuer
	Users               repository.UserRepository
	Packages            repository.PackageRepository
	Domains             repository.DomainRepository
	Databases           repository.DatabaseRepository
	DatabaseUsers       repository.DatabaseUserRepository
	DatabaseUserGrants  repository.DatabaseUserGrantRepository
	Agent               agent.AgentInterface
	Reconciler          *reconciler.Reconciler
	ServerSettings      repository.ServerSettingsRepository
	DNSZones            repository.DNSZoneRepository
	DNSRecords          repository.DNSRecordRepository
	SSLCerts            repository.SSLCertificateRepository
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

	api.RegisterServiceInfoRoute(r)
	api.RegisterHealthRoutes(r)
	if deps.Agent != nil {
		api.RegisterAgentHealthRoute(r, deps.Agent)
	}

	if deps.Auth != nil {
		api.RegisterAuthRoutes(r, api.AuthHandlerConfig{
			Service:            deps.Auth,
			AccessTTL:          cfg.Auth.AccessTTL,
			RefreshTTL:         cfg.Auth.RefreshTTL,
			CookieSecure:       cfg.CookieSecureResolved(),
			CookieSameSiteNone: false,
			StrictRateLimit:    rl.Strict(),
		})
	}

	if deps.JWTIssuer != nil {
		// Protected API group — everything under /api/v1/* except /auth
		// flows through RequireAuth.
		v1 := r.Group("/api/v1", middleware.RequireAuth(deps.JWTIssuer))
		api.RegisterMeRoutes(v1)
		if deps.Users != nil {
		api.RegisterUserRoutes(v1, api.UserHandlerConfig{
			Repo:            deps.Users,
			Agent:           deps.Agent,
			StrictRateLimit: rl.Strict(),
			Domains:         deps.Domains,
			Reconciler:      deps.Reconciler,
			AuthService:     deps.Auth,
			AccessTTL:       cfg.Auth.AccessTTL,
			RefreshTTL:      cfg.Auth.RefreshTTL,
			CookieName:      api.DefaultRefreshCookieName,
			CookieSecure:    cfg.CookieSecureResolved(),
			Log:             deps.Log,
		})
	}
		if deps.Packages != nil {
			api.RegisterPackageRoutes(v1, api.PackageHandlerConfig{Repo: deps.Packages})
		}
		if deps.Domains != nil {
			api.RegisterDomainRoutes(v1, api.DomainHandlerConfig{
				Domains:    deps.Domains,
				Users:      deps.Users,
				Packages:   deps.Packages,
				Agent:      deps.Agent,
				Reconciler: deps.Reconciler,
			})
		}
		if deps.Databases != nil && deps.DatabaseUsers != nil {
			api.RegisterDatabaseRoutes(v1, api.DatabaseHandlerConfig{
				Databases:     deps.Databases,
				DatabaseUsers: deps.DatabaseUsers,
				Users:         deps.Users,
				Packages:      deps.Packages,
				Agent:         deps.Agent,
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
		}

		// Admin routes
		if deps.Reconciler != nil {
			admin := v1.Group("/admin", middleware.RequireAdmin())
			api.RegisterReconcileRoutes(admin, &api.ReconcileHandlerConfig{
				Reconciler: deps.Reconciler,
				Log:        deps.Log,
			})
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
