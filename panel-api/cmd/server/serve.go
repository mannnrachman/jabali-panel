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

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/limits"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/app"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/db"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/reconciler"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
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

	// ---- auth + deps ----
	var deps app.Deps
	deps.Agent = sharedAgent
	deps.Log = log
	deps.SSOKey = ssoKeyPtr
	if sharedDB != nil {
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
		sshKeyRepo := repository.NewSSHKeyRepository(sharedDB)
		deps.SSHKeys = sshKeyRepo

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
		// When /home shares `/` (no dedicated partition), install.sh
		// deliberately skips enabling usrquota on the root filesystem
		// (see install.sh §install_quota: "quota-on-root is unsafe" —
		// a runaway user can exhaust the partition the OS itself
		// needs). QuotaMountFor still returns "/" in that case, so we
		// match install.sh's rule here: treat "/" as quota-disabled
		// rather than hand a mount path the kernel will reject. Agent
		// skips setquota when QuotaMount is empty (see
		// panel-agent/internal/commands/user_limits_apply_test.go §256).
		if m, err := limits.QuotaMountFor("/home"); err == nil {
			if m == "/" {
				if log != nil {
					log.Warn("m18: /home on root filesystem — disk-quota plumbing disabled (cgroups still active)")
				}
			} else {
				deps.QuotaMount = m
				rec.WithQuotaMount(m)
			}
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
			if ssoUDSShutdown != nil {
				if err := ssoUDSShutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Error("UDS server shutdown error", "err", err)
				}
			}
			if err := srv.Shutdown(shutdownCtx); err != nil {
				return err
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
