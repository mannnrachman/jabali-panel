// Package app wires HTTP routes, middleware and lifecycle together.
// Downstream packages (handlers, middleware, repositories) plug in here.
package app

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/apps"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
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
	Users               repository.UserRepository
	Packages            repository.PackageRepository
	Domains             repository.DomainRepository
	Databases           repository.DatabaseRepository
	DatabaseUsers       repository.DatabaseUserRepository
	DatabaseUserGrants  repository.DatabaseUserGrantRepository
	Mailboxes           repository.MailboxRepository
	MailboxSSOTokens    repository.MailboxSSOTokenRepository
	PhpMyAdminSSOTokens repository.PhpMyAdminSSOTokenRepository
	LogAccessStreams    repository.LogAccessStreamRepository
	Agent               agent.AgentInterface
	Reconciler          *reconciler.Reconciler
	ServerSettings      repository.ServerSettingsRepository
	PageTemplates       repository.PageTemplateRepository
	NotificationEventSettings repository.NotificationEventSettingRepository
	DNSZones            repository.DNSZoneRepository
	DNSRecords          repository.DNSRecordRepository
	SSLCerts            repository.SSLCertificateRepository
	PHPPools            repository.PHPPoolRepository
	PHPPoolIniOverrides repository.PHPPoolIniOverrideRepository
	WordPressInstalls   repository.WordPressInstallRepository
	// ManagedIPs is the M24 IP-pool repo. NewWithDeps registers
	// /admin/ips + /user/ips + /internal/agent/managed-ips when set;
	// nil keeps the routes off (lets existing test harnesses pass).
	ManagedIPs          repository.ManagedIPRepository
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
	// M6.5 email feature repositories. Autoresponders/Forwarders/MailboxShares
	// reconcile jabali intent → Stalwart via the phase registry (ADR-0051).
	Autoresponders      repository.EmailAutoresponderRepository
	Forwarders          repository.EmailForwarderRepository
	MailboxShares       repository.MailboxShareRepository
	// DNSSECKeys caches DNSSEC public-key metadata (ADR-0076).
	DNSSECKeys          repository.DNSSECKeyRepository
	// PanelCerts is the M32 singleton panel_certificate repo. NewWithDeps
	// registers /admin/panel-certificate when set; nil keeps the routes
	// off (lab installs / older test wiring).
	PanelCerts          repository.PanelCertificateRepository
	// M33 malware detection repos (ADR-0072). All five are wired
	// together — nil on any disables RegisterSecurityMalwareRoutes.
	// M30 backup-restore (ADR-0075). Nil disables the /admin/backups
	// + /me/backups routes; UI surfaces an empty state.
	BackupJobs          repository.BackupJobRepository
	// M30.1 backup destinations + schedules (ADR-0078). All three nil
	// disables the /admin/backup-destinations + /admin/backup-schedules
	// routes (UI tabs hidden) and the in-process scheduler / copy
	// worker / finalizer goroutines.
	BackupDestinations  repository.BackupDestinationRepository
	BackupSchedules     repository.BackupScheduleRepository
	MalwareQuarantine   repository.MalwareQuarantineRepository
	MalwareEvents       repository.MalwareEventRepository
	YARARules           repository.YARACustomRuleRepository
	MalwareSettings     repository.MalwareSettingsRepository
	// M33.2 mail YARA scanner — async tick walks Stalwart mailboxes via
	// JMAP, scans attachments with yr, quarantines hits. Off by default;
	// enabled via malware_settings.mail_scan_enabled. ADR-0079.
	MailScanState       repository.MailScanStateRepository
	MailScanFailures    repository.MailScanFailureRepository
	// MalwareUserScans tracks per-user manual-scan jobs (mig 000097).
	// Upserted at scan start by the panel-api startScan handler;
	// finalised by the post-scan-hook ingest path. Drives the Manual
	// Scan UI's Last scanned + Status columns.
	MalwareUserScans    repository.MalwareUserScanRepository
	// M34 per-user PHP-FPM egress firewall. Two repos backing the
	// nftables socket-cgroupv2 reconciler + admin/user request flow
	// (migrations 000100/000101, ADR-0084).
	UserEgressPolicies  repository.UserEgressPolicyRepository
	UserEgressRequests  repository.UserEgressRequestRepository
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
	// Redis is the shared *redis.Client for the notification dispatcher
	// (ADR-0056) and future WordPress object-cache (ADR-0059). Wired in
	// serve.go against cfg.Redis.URL; nil when Redis is disabled (tests,
	// or an operator who set Redis.URL=""). Handlers that need it must
	// guard for nil and return 503 rather than panic.
	Redis               *redis.Client
	// M14 notification repos. Wired by serve.go when sharedDB is set;
	// used by both the /admin/notifications admin API (added later) and
	// the dispatcher goroutine that drains the Redis stream.
	NotificationChannels repository.NotificationChannelRepository
	NotificationHistory  repository.NotificationHistoryRepository
	WebhookEndpoints     repository.WebhookEndpointRepository
	WebPushSubs          repository.WebPushSubscriptionRepository
	// NotificationQueue is the dispatcher's publish end — the internal
	// enqueue endpoint (RequireLocalhost) and in-process event sources
	// write to this Queue. Nil when Redis is not configured; handlers
	// must 503 rather than panic.
	NotificationQueue *notifications.Queue
	// DB is the raw GORM handle. Most handlers go through typed repos
	// — this is only here for tiny one-off endpoints that need a
	// COUNT(*) across multiple tables (e.g. /admin/counts on the
	// admin Dashboard) where adding a CountAll method to every repo
	// would be more code than the call site.
	// M41 Snuffleupagus PHP hardening — repository + reconciler + rules path
	Snuffleupagus           repository.SnuffleupagusRepository
	SnuffleupagusReconciler *reconciler.SnuffleupagusReconciler
	SnuffleupagusBundleDir  string
	DB *gorm.DB
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

	// M14 internal enqueue — /api/v1/internal/notifications/enqueue
	// bypasses the Kratos authMiddleware because the caller is the
	// agent (or an in-process event source) talking to us over the M25
	// loopback socket. RequireLocalhost inside the route is the sole
	// gate. Registered off the root router (not v1) so it doesn't pick
	// up RequireKratosSession.
	api.RegisterNotificationsInternalRoutes(r.Group("/api/v1"), deps.NotificationQueue)

	// M33 internal malware ingest — /api/v1/admin/security/malware/event.
	// Mounted off the root router (not v1) so it bypasses the Kratos
	// authMiddleware. Caller is the agent over the M25 unix socket;
	// RequireLocalhost inside the route is the sole gate. Matches the
	// RegisterNotificationsInternalRoutes pattern above.
	if deps.MalwareQuarantine != nil && deps.MalwareEvents != nil &&
		deps.MalwareSettings != nil && deps.YARARules != nil &&
		deps.Agent != nil {
		api.RegisterSecurityMalwareInternalRoutes(r.Group("/api/v1"), api.SecurityMalwareHandlerConfig{
			Agent:      deps.Agent,
			Quarantine: deps.MalwareQuarantine,
			Events:     deps.MalwareEvents,
			Settings:   deps.MalwareSettings,
			YARARules:  deps.YARARules,
			Users:      deps.Users,
			UserScans:  deps.MalwareUserScans,
			Log:        deps.Log,
		})
	}

	// M28 — public branding endpoints (logo file + brand text). Lives
	// off the root API group with NO auth so the pre-login page can
	// render the operator's logo + brand text.
	if deps.ServerSettings != nil {
		api.RegisterPublicBrandingRoutes(r.Group("/api/v1"), api.BrandingHandlerConfig{
			Repo: deps.ServerSettings,
			Log:  deps.Log,
		})
	}

	// Webmail SSO landing (M6 Step 8 Phase B). Lives at /sso/webmail on
	// the engine root, not /api/v1, because it's served from
	// mail.<domain> (via nginx location /sso) where the SPA + Kratos
	// prefix doesn't apply. Handler does its own token auth (consume +
	// hash match) so no Kratos cookie is required.
	if deps.Mailboxes != nil && deps.Domains != nil && deps.SSOKey != nil && deps.MailboxSSOTokens != nil {
		api.RegisterWebmailSSORoutes(r, api.WebmailSSOHandlerConfig{
			Mailboxes: deps.Mailboxes,
			Domains:   deps.Domains,
			SSOKey:    deps.SSOKey,
			SSOTokens: deps.MailboxSSOTokens,
			Log:       deps.Log,
		})
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

	if deps.KratosClient != nil {
		// Protected API group — everything under /api/v1/* flows through
		// RequireKratosSession, which resolves Kratos identity UUIDs →
		// panel user rows so claims.UserID carries the ULID every
		// ownership check in the API expects.
		authMiddleware := middleware.RequireKratosSession(deps.KratosClient, deps.Users)
		v1 := r.Group("/api/v1", authMiddleware)
		// M14 — fire one admin.login envelope per Kratos session.
		// Redis SETNX dedupes; downgrades to no-op without Redis/queue.
		v1.Use(middleware.TrackAdminLogin(deps.Redis, deps.NotificationQueue, deps.Log))
		api.RegisterMeRoutes(v1, api.MeHandlerConfig{
			Users:          deps.Users,
			ServerSettings: deps.ServerSettings,
		})

		if deps.Users != nil {
			api.RegisterUserRoutes(v1, api.UserHandlerConfig{
				Repo:            deps.Users,
				Agent:           deps.Agent,
				StrictRateLimit: rl.Strict(),
				Domains:         deps.Domains,
				Databases:       deps.Databases,
				DatabaseUsers:   deps.DatabaseUsers,
				Packages:        deps.Packages,
				Reconciler:      deps.Reconciler,
				Log:             deps.Log,
				// M20: atomic Kratos identity creation on POST /users.
				KratosClient: deps.KratosClient,
			})
		}
		if deps.Packages != nil {
			api.RegisterPackageRoutes(v1, api.PackageHandlerConfig{
				Repo:       deps.Packages,
				Users:      deps.Users,
				Reconciler: deps.Reconciler,
				Log:        deps.Log,
			})
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
				// DNS repos feed the auto-enable-email path in create.
				// Panel profiles without PowerDNS leave these nil and
				// create still works — auto-enable is skipped cleanly.
				DNSZones:   deps.DNSZones,
				DNSRecords: deps.DNSRecords,
				// M24: lets PATCH listen_ipv*_id resolve FK + family + the
				// is_user_selectable check, and lets GET denormalize
				// listen_ipv4 / listen_ipv6 onto each row. Optional —
				// when unset, the listen-IP fields are 503 on PATCH and
				// dropped from GET.
				ManagedIPs: deps.ManagedIPs,
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
				ServerSettings:    deps.ServerSettings,
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
		if deps.Mailboxes != nil && deps.Domains != nil {
			api.RegisterMailboxRoutes(v1, api.MailboxHandlerConfig{
				Mailboxes: deps.Mailboxes,
				Domains:   deps.Domains,
				Agent:     deps.Agent,
				SSOKey:    deps.SSOKey,
				SSOTokens: deps.MailboxSSOTokens,
			})
		}
		if deps.Domains != nil {
			api.RegisterDomainEmailRoutes(v1, api.DomainEmailHandlerConfig{
				Domains:       deps.Domains,
				Agent:         deps.Agent,
				DNSZones:      deps.DNSZones,
				DNSRecords:    deps.DNSRecords,
				SSLCerts:      deps.SSLCerts,
				SSLReconciler: deps.Reconciler,
			})
		}
		// M6.5 Email features: forwarders, autoresponders, catch-all, disclaimer,
		// shared folders, logs. All sub-routes live in routes_m65.go and are
		// filled in by Wave B/C steps (ADR-0051).
		api.RegisterM65Routes(v1, api.M65RouteDeps{
			Agent:          deps.Agent,
			Domains:        deps.Domains,
			Mailboxes:      deps.Mailboxes,
			Autoresponders: deps.Autoresponders,
			Forwarders:     deps.Forwarders,
			MailboxShares:  deps.MailboxShares,
		})
		// DNSSEC per-domain (ADR-0076). Standalone mount; not part of M6.5.
		api.RegisterDomainDNSSECRoutes(v1, api.DomainDNSSECHandlerConfig{
			Agent:   deps.Agent,
			Domains: deps.Domains,
			Keys:    deps.DNSSECKeys,
		})
		// Admin: System Updates (M29). Thin proxy to agent's system.* commands.
		api.RegisterAdminUpdatesRoutes(v1, api.AdminUpdatesHandlerConfig{
			Agent: deps.Agent,
		})
		// Admin: Support diagnostic report (M29, ADR-0064).
		api.RegisterAdminSupportRoutes(v1, api.AdminSupportHandlerConfig{
			Agent: deps.Agent,
		})
		// Admin: Server Status aggregator (M31, ADR-0065).
		api.RegisterAdminServerStatusRoutes(v1, api.AdminServerStatusHandlerConfig{
			Agent: deps.Agent,
			Redis: deps.Redis,
		})
		// Admin: Service controls (M31). Mounts POST /admin/services/:name/:action.
		api.RegisterAdminServicesRoutes(v1, api.AdminServicesHandlerConfig{
			Agent: deps.Agent,
		})
		// Admin: process kill (Server Status page). POST /admin/processes/:pid/kill.
		api.RegisterAdminProcessesRoutes(v1, api.AdminProcessesHandlerConfig{
			Agent: deps.Agent,
		})
		// Admin: dashboard counts (one-shot users/domains/mailboxes totals).
		api.RegisterAdminCountsRoutes(v1, api.AdminCountsHandlerConfig{
			DB: deps.DB,
		})
		// Admin: Panel SSL certificate (M32, ADR-0066). Skipped when
		// PanelCerts isn't wired (legacy test fixtures).
		if deps.PanelCerts != nil && deps.ServerSettings != nil {
			api.RegisterAdminPanelCertificateRoutes(v1, api.AdminPanelCertificateHandlerConfig{
				PanelCerts:     deps.PanelCerts,
				ServerSettings: deps.ServerSettings,
				Agent:          deps.Agent,
			})
		}
		if deps.Domains != nil && deps.DNSZones != nil && deps.DNSRecords != nil {
			api.RegisterDNSRoutes(v1, api.DNSHandlerConfig{
				Domains:        deps.Domains,
				Zones:          deps.DNSZones,
				Records:        deps.DNSRecords,
				ServerSettings: deps.ServerSettings,
				Reconciler:     deps.Reconciler,
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
			// M28 — admin logo upload/delete. Public GET lives on the
			// root router above so it's reachable pre-auth.
			api.RegisterBrandingRoutes(v1, api.BrandingHandlerConfig{
				Repo: deps.ServerSettings,
				Log:  deps.Log,
			})
		}
		if deps.PageTemplates != nil {
			api.RegisterPageTemplatesRoutes(v1, api.PageTemplatesHandlerConfig{
				Repo: deps.PageTemplates,
				Log:  deps.Log,
			})
		}
		if deps.NotificationEventSettings != nil {
			api.RegisterNotificationEventSettingsRoutes(v1, api.NotificationEventSettingsHandlerConfig{
				Repo: deps.NotificationEventSettings,
				Log:  deps.Log,
			})
		}
		// M6.4 Settings → Email: read-only panel-primary domain card.
		if deps.Domains != nil {
			api.RegisterSettingsEmailRoutes(v1, api.SettingsEmailHandlerConfig{
				Domains: deps.Domains,
				Log:     deps.Log,
			})
		}
		if deps.ManagedIPs != nil {
			// Step 4 flips AgentIPCommandsEnabled on: CRUD round-trips
			// through the agent's ip.bind / ip.unbind commands, and the
			// reconciler's managed-ip pass rebinds any DB-bound rows
			// the kernel has lost (host reboot between missing netplan
			// persistence and agent recovery).
			api.RegisterIPRoutes(v1, api.IPHandlerConfig{
				Repo:                   deps.ManagedIPs,
				Domains:                deps.Domains,
				Agent:                  deps.Agent,
				AgentIPCommandsEnabled: deps.Agent != nil,
				Log:                    deps.Log,
			})
		}

		// M14 Step 5 — admin channel CRUD + broadcast/test.
		if deps.NotificationChannels != nil {
			api.RegisterNotificationsChannelsRoutes(v1, api.NotificationsChannelsHandlerConfig{
				Channels:        deps.NotificationChannels,
				Webhooks:        deps.WebhookEndpoints,
				Queue:           deps.NotificationQueue,
				Log:             deps.Log,
				StrictRateLimit: rl.Strict(),
			})
		}
		// M31.1 follow-up — DLQ inspector (list / replay / drop / clear).
		api.RegisterNotificationsDLQRoutes(v1, api.NotificationsDLQHandlerConfig{
			Redis: deps.Redis,
			Log:   deps.Log,
		})
		// M14 Step 5 — authenticated-user bell dropdown.
		if deps.NotificationHistory != nil {
			api.RegisterNotificationsInboxRoutes(v1, api.NotificationsInboxHandlerConfig{
				History: deps.NotificationHistory,
				Redis:   deps.Redis,
				Log:     deps.Log,
			})
		}
		// M14 Step 5 — Web Push enrolment.
		if deps.WebPushSubs != nil && deps.ServerSettings != nil {
			api.RegisterNotificationsWebPushRoutes(v1, api.NotificationsWebPushHandlerConfig{
				ServerSettings: deps.ServerSettings,
				Subs:           deps.WebPushSubs,
				Channels:       deps.NotificationChannels,
				Log:            deps.Log,
			})
		}
		if deps.Agent != nil {
			api.RegisterSystemRoutes(v1, deps.Agent)
			api.RegisterPHPVersionRoutes(v1, deps.Agent)
			// M26 Step 4 — admin Security tab. CrowdSec + UFW are pure
			// agent passthroughs. ModSecurity removed 2026-04-26 (ADR-0055
			// SUPERSEDED) — CrowdSec AppSec covers the WAF role.
			api.RegisterSecurityCrowdSecRoutes(v1, deps.Agent, deps.ServerSettings)
			api.RegisterSecurityAppSecRoutes(v1, deps.Agent, deps.ServerSettings)
			api.RegisterSecurityUFWRoutes(v1, deps.Agent)
			// M40 (ADR-0086) AppArmor admin status + per-profile mode flip.
			api.RegisterSecurityAppArmorRoutes(v1, deps.Agent)
			// M42 (ADR-0087) AIDE FIM read-only status + manual check trigger.
			api.RegisterSecurityAideRoutes(v1, deps.Agent)
			// M33 malware tab — ADR-0072. All five malware repos must be
			// wired or RegisterSecurityMalwareRoutes is skipped (older test
			// wiring without the M33 graph). Tab still renders empty state.
			if deps.MalwareQuarantine != nil && deps.MalwareEvents != nil &&
				deps.MalwareSettings != nil && deps.YARARules != nil {
				api.RegisterSecurityMalwareRoutes(v1, api.SecurityMalwareHandlerConfig{
					Agent:      deps.Agent,
					Quarantine: deps.MalwareQuarantine,
					Events:     deps.MalwareEvents,
					Settings:   deps.MalwareSettings,
					YARARules:  deps.YARARules,
					Users:      deps.Users,
					UserScans:  deps.MalwareUserScans,
					Log:        deps.Log,
				})
			}
		}
		if deps.BackupJobs != nil && deps.Users != nil {
			api.RegisterBackupRoutes(v1, api.BackupHandlerConfig{
				Agent:          deps.Agent,
				Jobs:           deps.BackupJobs,
				Destinations:   deps.BackupDestinations,
				Users:          deps.Users,
				Databases:      deps.Databases,
				DatabaseUsers:  deps.DatabaseUsers,
				DatabaseGrants: deps.DatabaseUserGrants,
				Domains:        deps.Domains,
				Mailboxes:      deps.Mailboxes,
				AppInstalls:    deps.WordPressInstalls,
				Log:            deps.Log,
			})
			api.RegisterMeBackupRoutes(v1, api.MeBackupsHandlerConfig{
				Agent:          deps.Agent,
				Jobs:           deps.BackupJobs,
				Users:          deps.Users,
				Databases:      deps.Databases,
				DatabaseUsers:  deps.DatabaseUsers,
				DatabaseGrants: deps.DatabaseUserGrants,
				Domains:        deps.Domains,
				Mailboxes:      deps.Mailboxes,
				AppInstalls:    deps.WordPressInstalls,
				Log:            deps.Log,
			})
		}
		// M30.1 (ADR-0078): backup destinations + schedules.
		if deps.BackupDestinations != nil {
			api.RegisterBackupDestinationRoutes(v1, api.BackupDestinationsConfig{
				Repo:  deps.BackupDestinations,
				Agent: deps.Agent,
			})
			// SSH-key listing/generate endpoints feed the SFTP key
			// dropdown on the destinations drawer. Both ops run as
			// root via the agent (panel-api can't read /root/.ssh
			// or write /etc/jabali-panel/restic-remotes/).
			api.RegisterSystemSSHKeysRoutes(v1, api.SystemSSHKeysConfig{
				Agent: deps.Agent,
			})
			// Master restic password reveal — admin-only. Per
			// ADR-0075 operators must back the password up
			// out-of-band; losing it loses every snapshot.
			api.RegisterBackupEncryptionKeyRoutes(v1, api.BackupEncryptionKeyConfig{
				Agent: deps.Agent,
			})
		}
		if deps.BackupSchedules != nil && deps.BackupDestinations != nil {
			api.RegisterBackupScheduleRoutes(v1, api.BackupSchedulesConfig{
				Schedules:    deps.BackupSchedules,
				Destinations: deps.BackupDestinations,
				Users:        deps.Users,
				Jobs:         deps.BackupJobs,
			})
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
			}
			api.RegisterWordPressRoutes(v1, appCfg)
			api.RegisterApplicationRoutes(v1, appCfg)

		// Log access routes (M13)
		if deps.LogAccessStreams != nil && deps.Domains != nil && deps.Users != nil {
			api.RegisterLogRoutes(v1, api.LogHandlerConfig{
				LogAccessStreams: deps.LogAccessStreams,
				Domains:          deps.Domains,
				Users:            deps.Users,
			})
			// WebSocket log streaming routes
			api.RegisterLogStreamRoutes(v1, api.LogStreamHandlerConfig{
				LogAccessStreams: deps.LogAccessStreams,
				Domains:          deps.Domains,
				Log:              deps.Log,
			})
		}
		}

		// Magic-link admin login (ADR-0040): mint-only. The agent-written
		// PHP file is its own validator, so no validate endpoint lives here.
		if deps.WordPressInstalls != nil && deps.Domains != nil &&
			deps.Users != nil && deps.Agent != nil {
			api.RegisterMagicLinkRoutes(v1, api.MagicLinkHandlerConfig{
				ApplicationInstalls: deps.WordPressInstalls,
				Domains:             deps.Domains,
				Users:               deps.Users,
				Agent:               deps.Agent,
			})
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
				Users:          deps.Users,
				Domains:        deps.Domains,
				Agent:          deps.Agent,
				Log:            deps.Log,
				ServerSettings: deps.ServerSettings,
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
		// M34 per-user PHP-FPM egress firewall — admin + user routes share
		// the same handler config; admin half mounts under /admin
		// (RequireAdmin) and user half under the auth-only base group so
		// /me/* resolves to the caller via JWT.
		if deps.Users != nil && deps.UserEgressPolicies != nil && deps.UserEgressRequests != nil {
			adminEgress := v1.Group("/admin", middleware.RequireAdmin())
			cfg := api.UserEgressHandlerConfig{
				Users:    deps.Users,
				Policies: deps.UserEgressPolicies,
				Requests: deps.UserEgressRequests,
			}
			api.RegisterAdminUserEgressRoutes(adminEgress, cfg)
			api.RegisterMeEgressRoutes(v1, cfg)
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
