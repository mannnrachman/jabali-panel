package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/limits"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/app"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/db"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/eventsources"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications/senders"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/reconciler"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/services"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/sso"
)

const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 90 * time.Second
	shutdownTimeout   = 10 * time.Second
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the Jabali Panel HTTP(S) server",
		RunE:  runServe,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := initConfig(); err != nil {
				return err
			}
			return nil
		},
	}
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg := sharedCfg
	log := sharedLog

	log.Info("starting jabali-panel",
		"addr", cfg.Server.Addr,
		"env", cfg.Server.Env,
	)

	// ---- DB ----
	if cfg.Database.URL != "" && cfg.Database.URL != "placeholder-until-phase-3" {
		if os.Getenv("SKIP_MIGRATIONS") != "true" {
			if err := db.Migrate(cfg.Database.URL); err != nil {
				return err
			}
			log.Info("migrations up-to-date")
		}
		if err := initDB(); err != nil {
			return err
		}
		log.Info("db connected")
	} else {
		log.Warn("DATABASE_URL not set; running without DB")
	}

	// ---- agent ----
	if err := initAgent(); err != nil {
		return err
	}
	log.Info("agent client configured", "socket", cfg.Agent.SocketPath)

	// ---- SSO key ----
	// Deps.SSOKey stays nil when the key file is absent; the SSO handler
	// refuses requests in that state so the feature is opt-in without
	// blocking startup on a missing file.
	// Use a pointer to allow hot-reloading on SIGHUP.
	ssoKeyPtr := loadSSOKey(cfg.SSO.KeyPath, log)

	// ---- Redis ----
	// ADR-0056 dispatcher + ADR-0059 shared cache client. Parse the URL
	// (unix:// or redis://), open a single connection pool, ping with a
	// short timeout to fail-fast if Redis is unreachable. Missing Redis
	// is fatal in production — the notification dispatcher must have
	// its queue — but configurable-out so tests can run without Redis.
	var redisClient *redis.Client
	if cfg.Redis.URL != "" {
		opts, err := redis.ParseURL(cfg.Redis.URL)
		if err != nil {
			return fmt.Errorf("parse redis url %q: %w", cfg.Redis.URL, err)
		}
		redisClient = redis.NewClient(opts)
		pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := redisClient.Ping(pingCtx).Err(); err != nil {
			return fmt.Errorf("redis ping %q: %w (is redis-server up? install_redis() sets up /run/redis/redis.sock)", cfg.Redis.URL, err)
		}
		log.Info("redis connected", "url", cfg.Redis.URL)
	} else {
		log.Warn("redis URL not set; notification dispatcher will be disabled")
	}

	// ---- auth + deps ----
	var deps app.Deps
	deps.Agent = sharedAgent
	deps.Log = log
	deps.SSOKey = ssoKeyPtr
	deps.Redis = redisClient
	if sharedDB != nil {
		deps.DB = sharedDB
		userRepo := repository.NewUserRepository(sharedDB)
		packageRepo := repository.NewPackageRepository(sharedDB)
		domainRepo := repository.NewDomainRepository(sharedDB)
		dnsZoneRepo := repository.NewDNSZoneRepository(sharedDB)
		dnsRecordRepo := repository.NewDNSRecordRepository(sharedDB)
		sslCertRepo := repository.NewSSLCertificateRepository(sharedDB)
		databaseRepo := repository.NewDatabaseRepository(sharedDB)
		databaseUserRepo := repository.NewDatabaseUserRepository(sharedDB)
		databaseUserGrantRepo := repository.NewDatabaseUserGrantRepository(sharedDB)
		mailboxRepo := repository.NewMailboxRepository(sharedDB)
		mailboxSSOTokenRepo := repository.NewMailboxSSOTokenRepository(sharedDB)

		phpMyAdminSSOTokenRepo := repository.NewPhpMyAdminSSOTokenRepository(sharedDB)
		phpPoolRepo := repository.NewPHPPoolRepository(sharedDB)
		phpPoolIniOverrideRepo := repository.NewPHPPoolIniOverrideRepository(sharedDB)
		wordpressInstallRepo := repository.NewWordPressInstallRepository(sharedDB)
		cronJobsRepo := repository.NewCronJobRepository(sharedDB)
		limitOverridesRepo := repository.NewUserLimitOverrideRepository(sharedDB)

		serverSettingsRepo := repository.NewServerSettingsRepository(sharedDB)
		pageTemplateRepo := repository.NewPageTemplateRepository(sharedDB)
		notificationEventSettingRepo := repository.NewNotificationEventSettingRepository(sharedDB)

		// SSO service for phpMyAdmin
		ssoService := sso.NewService(
			sharedDB,
			userRepo,
			phpMyAdminSSOTokenRepo,
			sharedAgent,
			ssoKeyPtr,
			log,
		)
		deps.Users = userRepo
		deps.Packages = packageRepo
		deps.Domains = domainRepo
		deps.SSO = ssoService

		deps.ServerSettings = serverSettingsRepo
		deps.PageTemplates = pageTemplateRepo
		deps.NotificationEventSettings = notificationEventSettingRepo
		sshKeyRepo := repository.NewSSHKeyRepository(sharedDB)
		deps.SSHKeys = sshKeyRepo

		// M14 notification repos. Populated whenever sharedDB is up;
		// the admin API (later) and dispatcher goroutine both read them
		// from deps. Dispatcher only starts when cfg.Redis.URL resolved
		// and at least one ChannelSender is registered (Step 3 adds the
		// concrete senders — slack, ntfy, webhook, webpush, email).
		deps.NotificationChannels = repository.NewNotificationChannelRepository(sharedDB)
		deps.NotificationHistory = repository.NewNotificationHistoryRepository(sharedDB)
		deps.WebhookEndpoints = repository.NewWebhookEndpointRepository(sharedDB)
		deps.WebPushSubs = repository.NewWebPushSubscriptionRepository(sharedDB)
		if redisClient != nil {
			// NotificationQueue is the publish end. RegisterNotificationsInternalRoutes
			// takes this same pointer so the agent's notifications.send
			// command and in-process event sources (Step 4 below) write to
			// the identical Redis stream the dispatcher drains.
			deps.NotificationQueue = notifications.NewQueue(redisClient)
		}

		// Reconciler: database as source of truth, agent state as derived state.
		rec := reconciler.New(
			domainRepo,
			userRepo,
			sharedAgent,
			sharedLog,
			reconciler.Config{
				Interval: cfg.Agent.ReconcilerInterval,
				QueueLen: 100,
			},
		)
		rec.WithDNSRepos(dnsZoneRepo, dnsRecordRepo, serverSettingsRepo)
		rec.WithSSLCerts(sslCertRepo)
		rec.WithPHPPools(phpPoolRepo)
		rec.WithConfig(cfg)
		rec.WithSSO(ssoService)
		rec.WithSSHKeys(sshKeyRepo)
		rec.WithCronJobs(cronJobsRepo)
		// M18 wiring — packages + overrides + /home mount path so
		// ReconcileUserLimits and ReconcileNginxRateLimits have every
		// dep they need. Mount path resolved below after deps are set.
		rec.WithPackages(packageRepo)
		rec.WithLimitOverrides(limitOverridesRepo)
		managedIPRepo := repository.NewManagedIPRepository(sharedDB)
		rec.WithManagedIPs(managedIPRepo)
		rec.WithPageTemplates(pageTemplateRepo)
		deps.ManagedIPs = managedIPRepo
		deps.Reconciler = rec
		deps.DNSZones = dnsZoneRepo
		deps.DNSRecords = dnsRecordRepo
		deps.SSLCerts = sslCertRepo
		deps.Databases = databaseRepo
		deps.DatabaseUsers = databaseUserRepo
		deps.DatabaseUserGrants = databaseUserGrantRepo
		deps.Mailboxes = mailboxRepo
		deps.MailboxSSOTokens = mailboxSSOTokenRepo
		// M6.5 repos. Each step's wave adds the concrete constructor as
		// it ships; Autoresponders ships in Step 3, Forwarders in Step 5,
		// MailboxShares in Step 4. Until each lands, nil + handler guards.
		deps.Autoresponders = repository.NewEmailAutoresponderRepository(sharedDB)
		deps.MailboxShares = repository.NewMailboxShareRepository(sharedDB)
		deps.Forwarders = repository.NewEmailForwarderRepository(sharedDB)
		deps.DNSSECKeys = repository.NewDNSSECKeyRepository(sharedDB)
		// M32 (ADR-0066): singleton panel_certificate repo + reconciler.
		// Without this wiring, /admin/panel-certificate routes are
		// skipped (RegisterAdminPanelCertificateRoutes returns early
		// when PanelCerts is nil) and the Server Settings → General
		// → Panel SSL card 404s with "Failed to load panel SSL state".
		// Surfaced 2026-04-26 on first VPS install at mx.jabali-panel.com.
		panelCertRepo := repository.NewPanelCertificateRepository(sharedDB)
		deps.PanelCerts = panelCertRepo
		rec.WithPanelCertificate(panelCertRepo, services.NewPanelCertRoutability())

		// M33 (ADR-0067): malware detection repos. Five repos wired
		// together — RegisterSecurityMalwareRoutes activates only when
		// all five are non-nil. Idempotent EnsureDefault on first /settings
		// access seeds the singleton row if migration 000076 hasn't run.
		deps.MalwareQuarantine = repository.NewMalwareQuarantineRepository(sharedDB)
		deps.MalwareEvents = repository.NewMalwareEventRepository(sharedDB)
		deps.MalwareSettings = repository.NewMalwareSettingsRepository(sharedDB)
		deps.YARARules = repository.NewYARACustomRuleRepository(sharedDB)
		deps.TetragonPolicies = repository.NewTetragonPolicyStateRepository(sharedDB)
		deps.PhpMyAdminSSOTokens = phpMyAdminSSOTokenRepo
		deps.PHPPools = phpPoolRepo
		deps.PHPPoolIniOverrides = phpPoolIniOverrideRepo
		deps.WordPressInstalls = wordpressInstallRepo
		deps.CronJobs = cronJobsRepo
		deps.LimitOverrides = limitOverridesRepo

		// M18: resolve the /home mount once at startup. Passed to every
		// user.limits.{apply,clear,report} call so the agent runs
		// setquota against the explicit mount path, never -a. Failure
		// degrades gracefully — QuotaMount=="" disables the disk half
		// of the pipeline while cgroups enforcement still works.
		//
		// /home==/ is supported (matches cPanel/DA): install.sh enables
		// ext4 hidden quota inodes on / via tune2fs -O quota; the
		// reconciler only ever calls setquota for hosting UIDs (>=1000)
		// so root and system daemons stay unlimited regardless of which
		// filesystem holds the quota tables. Whether quota_mount is
		// honored at runtime is a separate question, gated by
		// server_settings.disk_quota_enabled (admin UI toggle).
		if m, err := limits.QuotaMountFor("/home"); err == nil {
			deps.QuotaMount = m
			rec.WithQuotaMount(m)
		} else if log != nil {
			log.Warn("m18: could not resolve /home mount; disk-quota plumbing disabled", "err", err)
		}

		// Admin bootstrap — atomic panel row + Kratos identity. Wait up to
		// 60s for Kratos to answer /health/ready first: install.sh starts
		// jabali-kratos immediately before jabali-panel, and the panel can
		// beat Kratos to binding :4434 on a slow boot. If Kratos never
		// answers, we bootstrap the panel DB row only and log a pointer
		// to `jabali kratos-migrate` so the operator can backfill once
		// Kratos is healthy — crashing the panel here would be worse.
		var bootstrapKratos auth.KratosIdentityWriter
		if cfg.Auth.Kratos.PublicURL != "" {
			// Build the client first so the readiness poll inherits the
			// configured transport (unix-socket dialer for M25 admin
			// endpoints). Reusing the bootstrap client also halves the
			// transport count in the steady state.
			candidate := kratosclient.NewClient(cfg.Auth.Kratos.PublicURL, cfg.Auth.Kratos.AdminURL)
			if waitForKratosReady(candidate, 60*time.Second, log) {
				bootstrapKratos = candidate
			} else {
				log.Warn("Kratos not ready after 60s — bootstrapping admin in panel DB only",
					"admin_url", cfg.Auth.Kratos.AdminURL,
					"action", "run 'jabali kratos-migrate' after Kratos is healthy")
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		res, err := auth.BootstrapAdmin(ctx, userRepo, auth.BootstrapOptions{
			Email:      os.Getenv("JABALI_BOOTSTRAP_ADMIN_EMAIL"),
			Password:   os.Getenv("JABALI_BOOTSTRAP_ADMIN_PASSWORD"),
			BcryptCost: bcrypt.DefaultCost,
			Kratos:     bootstrapKratos,
		})
		cancel()
		switch {
		case err != nil:
			return err
		case res.Created:
			log.Warn("admin user created via bootstrap",
				"kratos_identity_id", res.KratosIdentityID)
			// Welcome bell row — fired exactly once, on the boot that
			// creates the admin row. Written directly to
			// notification_history (channel_id=NULL, user_id=admin) so
			// it appears in the bell even on a fresh install with no
			// channels configured (the dispatcher would ack-and-drop
			// envelopes whose target list is empty). Best-effort: any
			// failure here should not block panel start.
			if deps.NotificationHistory != nil && res.UserID != "" {
				now := time.Now().UTC()
				row := &models.NotificationHistory{
					ID:        ids.NewULID(),
					EventKind: "panel.welcome",
					Severity:  models.NotificationSeverityInfo,
					Title:     "Welcome to Jabali Panel",
					Body:      "Set up notification channels (email, Slack, ntfy, webhook, web push) so you hear about cert renewals, disk pressure, and security events. Tap to open Notifications.",
					Deeplink:  "/jabali-admin/notifications/channels",
					Outcome:   models.NotificationOutcomeSent,
					UserID:    &res.UserID,
					CreatedAt: now,
					UpdatedAt: now,
				}
				wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
				if wErr := deps.NotificationHistory.Create(wctx, row); wErr != nil {
					log.Warn("welcome bell row insert failed", "err", wErr)
				}
				wcancel()
			}
		case res.ExistingID != "":
			log.Info("admin bootstrap: already exists",
				"user_id", res.ExistingID,
				"kratos_identity_id", res.KratosIdentityID)
		}

		// Merge-seed server_settings from config.toml [server] block on
		// every boot. Operator edits via the admin API win — once a field
		// has a non-empty value in the DB, config won't overwrite it. But
		// empty DB fields get filled from config whenever config has a
		// value. This way a partial earlier seed (e.g. from a boot where
		// config.toml was still broken) gets repaired the next time the
		// operator re-runs install.sh with a valid config.
		func() {
			seedCtx, seedCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer seedCancel()

			row, getErr := serverSettingsRepo.Get(seedCtx)
			created := false
			if errors.Is(getErr, repository.ErrNotFound) {
				row = &models.ServerSettings{ID: 1}
				created = true
			} else if getErr != nil {
				log.Error("server_settings probe failed", "err", getErr)
				return
			}

			mutated := created
			fillIfEmpty := func(target *string, source string) {
				if *target == "" && source != "" {
					*target = source
					mutated = true
				}
			}
			fillIfEmpty(&row.Hostname, cfg.Server.Hostname)
			fillIfEmpty(&row.PublicIPv4, cfg.Server.PublicIPv4)
			fillIfEmpty(&row.PublicIPv6, cfg.Server.PublicIPv6)
			fillIfEmpty(&row.NS1Name, cfg.Server.NS1Name)
			fillIfEmpty(&row.NS1IPv4, cfg.Server.NS1IPv4)
			fillIfEmpty(&row.NS2Name, cfg.Server.NS2Name)
			fillIfEmpty(&row.NS2IPv4, cfg.Server.NS2IPv4)
			// admin_email seeds from the bootstrap env var install.sh writes
			// to /etc/jabali/panel.env. Without this, fresh installs start
			// with admin_email="" which the SSL reconciler treats as
			// "ACME not configured" → every domain sits on self-signed
			// forever, retrying every 3h only to skip straight to fallback
			// again. Mirrors JABALI_BOOTSTRAP_ADMIN_{EMAIL,PASSWORD} already
			// read by auth.BootstrapAdmin.
			fillIfEmpty(&row.AdminEmail, os.Getenv("JABALI_BOOTSTRAP_ADMIN_EMAIL"))
			// M13: stamp the latest default sandbox image so a row left
			// behind by an older release (or one whose UI clobbered it
			// with an obsolete pin) self-heals on next boot. Migration
			// 000078 already bumps debian-12-v1 → debian-13-v1, but only
			// matches that exact value; this catches NULL/"" too.
			fillIfEmpty(&row.DefaultNspawnImageVersion, "debian-13-v1")

			if !mutated {
				return
			}
			if err := serverSettingsRepo.Upsert(seedCtx, row); err != nil {
				log.Error("failed to seed server_settings from config", "err", err)
				return
			}
			if created {
				log.Info("seeded server_settings from config.toml", "hostname", row.Hostname)
			} else {
				log.Info("merged missing server_settings fields from config.toml", "hostname", row.Hostname)
			}

			// M24 first-boot seed: materialise the is_default managed_ips
			// row(s) from the freshly-populated server_settings. Migration
			// 000057 can't do this because it runs (via db.Migrate above)
			// BEFORE this seed goroutine executes — install.sh populates
			// server_settings via cfg, not a direct DB write. Keeping the
			// seed here means the default row always lives alongside the
			// server_settings row it mirrors, and neither migration 57
			// nor install.sh needs to know about the other.
			if managedIPRepo != nil {
				if err := managedIPRepo.EnsureDefault(seedCtx, row.PublicIPv4, "ipv4"); err != nil {
					log.Error("failed to seed default managed_ips ipv4 row", "err", err)
				}
				if err := managedIPRepo.EnsureDefault(seedCtx, row.PublicIPv6, "ipv6"); err != nil {
					log.Error("failed to seed default managed_ips ipv6 row", "err", err)
				}
			}

			// M14 first-boot seed: generate the installation-global
			// VAPID keypair if not yet present (ADR-0057). Reuses the
			// same seed goroutine as managed_ips because it has the
			// same ordering constraint — migration 000065 added the
			// columns but can't populate them (per
			// feedback_migration_data_seed_ordering); the keys exist
			// only after this runs. Non-fatal on error: the Web Push
			// sender will skip channels when the keypair is missing,
			// which surfaces as a clear log event rather than
			// crash-looping the whole panel.
			if generated, err := serverSettingsRepo.EnsureVAPID(seedCtx, row.Hostname); err != nil {
				log.Error("failed to seed VAPID keypair", "err", err)
			} else if generated {
				log.Info("generated VAPID keypair for Web Push", "subject_host", row.Hostname)
			}

			// M28 first-boot seed: populate page_templates with the
			// baked-in default bodies for keys operators can later
			// override (domain default index, error pages). Migration
			// 000068 only creates the table; rows live here per
			// feedback_migration_data_seed_ordering.
			if pageTemplateRepo != nil {
				if seeded, err := pageTemplateRepo.EnsureDefaults(seedCtx); err != nil {
					log.Error("failed to seed page_templates defaults", "err", err)
				} else if seeded > 0 {
					log.Info("seeded default page_templates", "count", seeded)
				}
			}

			// M14.1 first-boot seed: per-event-kind enable toggles.
			// Defaults defined in models.AllNotificationEventKinds —
			// "important = on" (cert renewal failures, expiry, disk
			// crit, service down, backup fail, channel auto-disabled),
			// rest off.
			if notificationEventSettingRepo != nil {
				if seeded, err := notificationEventSettingRepo.EnsureDefaults(seedCtx); err != nil {
					log.Error("failed to seed notification_event_settings defaults", "err", err)
				} else if seeded > 0 {
					log.Info("seeded default notification event toggles", "count", seeded)
				}
			}
		}()
	}

	// ---- HTTP(S) ----
	handler := app.NewWithDeps(cfg, deps)

	// M25 Step 4: open the listener up front so we know whether we got a
	// TCP socket (TLS branch still applies) or a Unix socket (TLS is
	// stripped — nginx terminates real TLS upstream of us). Stale-socket
	// cleanup, chmod 0660, and chgrp jabali-sockets all happen inside
	// listenAndPrepare so the fragile bits live in one tested place.
	listener, isUnix, err := listenAndPrepare(cfg.Server.Addr)
	if err != nil {
		return err
	}
	useTLS := !isUnix && cfg.Server.TLSCert != "" && cfg.Server.TLSKey != ""
	if isUnix && (cfg.Server.TLSCert != "" || cfg.Server.TLSKey != "") {
		log.Warn("TLS configured but listening on Unix socket — TLS keys ignored",
			"addr", cfg.Server.Addr,
			"hint", "nginx terminates TLS; remove tls_cert/tls_key from config to silence this warning")
	}

	srv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// Start reconciler background loop if it's configured.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure cancel is called on all exit paths
	if deps.Reconciler != nil {
		go deps.Reconciler.Start(ctx)
	}

	// M14 notification dispatcher. Starts iff we have Redis + all
	// notification repos + at least one ChannelSender registered.
	// Step 3 (senders) lands slack/ntfy/webhook/webpush/email — until
	// then the registry is empty and we skip startup with a loud log,
	// keeping the pipeline in an observable "inactive" state rather
	// than silently dropping events.
	dispatcherDone, dispatcherStop := startNotificationDispatcher(ctx, deps, log)

	// M14 Step 4 event sources. These are cheap goroutines that poll
	// system state (SSL certs, disk usage, systemd units, CrowdSec
	// decisions) and publish envelopes on the same Queue the internal
	// enqueue endpoint + agent use. All respect ctx.Done, so SIGTERM
	// stops them alongside the dispatcher.
	if deps.NotificationQueue != nil {
		eventsources.Start(ctx, eventsources.Deps{
			Queue:      deps.NotificationQueue,
			History:    deps.NotificationHistory,
			SSLCerts:   deps.SSLCerts,
			Log:        log,
			Users:      deps.Users,
			Agent:      deps.Agent,
			QuotaMount: deps.QuotaMount,
		})
	}


	var ssoUDSShutdown func(context.Context) error
	if ssoKeyPtr != nil && cfg.SSO.SocketPath != "" {
		var err error
		_, ssoUDSShutdown, err = app.StartSSOUDSListener(
			cfg.SSO.SocketPath,
			deps.Databases,
			deps.Users,
			deps.PhpMyAdminSSOTokens,
			ssoKeyPtr,
			log,
		)
		if err != nil {
			return fmt.Errorf("start SSO UDS listener: %w", err)
		}
	}
	serveErr := make(chan error, 1)
	switch {
	case useTLS:
		log.Info("TLS enabled", "cert", cfg.Server.TLSCert, "key", cfg.Server.TLSKey, "addr", cfg.Server.Addr)
		go func() { serveErr <- srv.ServeTLS(listener, cfg.Server.TLSCert, cfg.Server.TLSKey) }()
		go startHTTPRedirect(cfg.Server.Addr, log)
	case isUnix:
		log.Info("listening on Unix socket — nginx terminates TLS upstream", "addr", cfg.Server.Addr)
		go func() { serveErr <- srv.Serve(listener) }()
	default:
		log.Warn("TLS not configured — serving plain HTTP", "addr", cfg.Server.Addr)
		go func() { serveErr <- srv.Serve(listener) }()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	for {
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		case sig := <-stop:
			if sig == syscall.SIGHUP {
				// Hot-reload SSO key without restarting
				log.Info("SIGHUP received, reloading SSO key")
				newKey := loadSSOKey(cfg.SSO.KeyPath, log)
				if newKey != nil {
					*ssoKeyPtr = *newKey
					log.Info("SSO key reloaded successfully")
					deps.SSOKey = ssoKeyPtr
				}
				continue // Continue serving
			}
			// SIGINT or SIGTERM — shutdown
			log.Info("shutdown signal", "signal", sig.String())
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer shutdownCancel()
			// Stop the notification dispatcher before HTTP so new
			// publishes funnel through while in-flight XACKs drain.
			if dispatcherStop != nil {
				dispatcherStop()
			}
			if ssoUDSShutdown != nil {
				if err := ssoUDSShutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Error("UDS server shutdown error", "err", err)
				}
			}
			if err := srv.Shutdown(shutdownCtx); err != nil {
				return err
			}
			// Wait for the dispatcher's drain to finish (or the grace
			// window to close) — guarantees we don't exit while
			// XREADGROUP still holds entries in its PEL.
			if dispatcherDone != nil {
				select {
				case <-dispatcherDone:
				case <-shutdownCtx.Done():
				}
			}
			log.Info("jabali-panel stopped")
			return nil
		}
	}
}

// loadSSOKey attempts to load the SSO encryption key from disk.
// Returns nil if the key file is missing or an error occurs.
func loadSSOKey(keyPath string, log *slog.Logger) *ssokey.Key {
	if keyPath == "" {
		return nil
	}
	ssoKey, err := ssokey.Load(keyPath)
	if err == nil {
		log.Info("SSO key loaded", "path", keyPath)
		return &ssoKey
	}
	if errors.Is(err, ssokey.ErrKeyMissing) {
		log.Warn("SSO key not found (phpMyAdmin SSO disabled)", "path", keyPath)
		return nil
	}
	log.Error("failed to load SSO key", "path", keyPath, "err", err)
	return nil
}

// startNotificationDispatcher wires the M14 Redis Streams consumer. It
// returns a done channel that closes once Start() returns, and a stop
// function the shutdown path calls to cancel the dispatcher context.
// Both are nil when the dispatcher is not started (no Redis, no repos,
// or no registered ChannelSenders — the registry is empty until Step 3
// wires the concrete senders).
func startNotificationDispatcher(parent context.Context, deps app.Deps, log *slog.Logger) (done <-chan struct{}, stop func()) {
	if deps.Redis == nil {
		log.Warn("notifications dispatcher: not starting — Redis not configured")
		return nil, nil
	}
	if deps.NotificationChannels == nil || deps.NotificationHistory == nil || deps.WebhookEndpoints == nil {
		log.Warn("notifications dispatcher: not starting — notification repos missing")
		return nil, nil
	}
	registry := notifications.NewRegistry()
	registry.Register(senders.NewSlack())
	registry.Register(senders.NewDiscord())
	registry.Register(senders.NewNtfy())
	registry.Register(senders.NewWebhook())
	registry.Register(senders.NewEmail("127.0.0.1:587"))
	// WebPush reads VAPID keys from server_settings on every send; if
	// the keypair is absent (EnsureVAPID hasn't run) Send returns
	// ErrPermanent and the envelope lands in the DLQ — surfaces as a
	// clear operator signal instead of silently dropping.
	if deps.ServerSettings != nil && deps.WebPushSubs != nil {
		registry.Register(senders.NewWebPush(deps.ServerSettings, deps.WebPushSubs, log))
	} else {
		log.Warn("notifications dispatcher: webpush sender skipped — server_settings or webpush_subscriptions repo missing")
	}
	queue := deps.NotificationQueue
	if queue == nil {
		queue = notifications.NewQueue(deps.Redis)
	}
	d, err := notifications.NewDispatcher(
		queue, registry,
		deps.NotificationChannels, deps.NotificationHistory, deps.WebhookEndpoints,
		log, notifications.Config{},
	)
	if err == nil && deps.NotificationEventSettings != nil {
		d.WithEventSettings(deps.NotificationEventSettings)
	}
	if err == nil && deps.Users != nil {
		d.WithUsers(deps.Users)
	}
	if err != nil {
		log.Error("notifications dispatcher: construction failed", "err", err)
		return nil, nil
	}
	dispatchCtx, dispatchCancel := context.WithCancel(parent)
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		if err := d.Start(dispatchCtx); err != nil {
			log.Error("notifications dispatcher: start failed", "err", err)
		}
	}()
	return doneCh, dispatchCancel
}

func startHTTPRedirect(httpsAddr string, log interface{ Debug(string, ...any) }) {
	_, port, _ := net.SplitHostPort(httpsAddr)
	if port == "" {
		port = "8443"
	}
	redirect := &http.Server{
		Addr:              ":80",
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			target := "https://" + host
			if port != "443" {
				target += ":" + port
			}
			target += r.URL.RequestURI()
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		}),
	}
	if err := redirect.ListenAndServe(); err != nil {
		log.Debug("HTTP→HTTPS redirect listener failed", "err", err)
	}
}
