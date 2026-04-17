package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/app"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/db"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/reconciler"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
)

const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 90 * time.Second
	shutdownTimeout   = 10 * time.Second

	jwtIssuerName = "jabali-panel"
	jwtKeyID      = "v1"
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
	var ssoKeyPtr *ssokey.Key
	if ssoKey, err := ssokey.Load(cfg.SSO.KeyPath); err == nil {
		k := ssoKey
		ssoKeyPtr = &k
		log.Info("SSO key loaded", "path", cfg.SSO.KeyPath)
	} else if errors.Is(err, ssokey.ErrKeyMissing) {
		log.Warn("SSO key not found (phpMyAdmin SSO disabled)", "path", cfg.SSO.KeyPath)
	} else {
		return fmt.Errorf("failed to load SSO key: %w", err)
	}

	// ---- auth + deps ----
	var deps app.Deps
	deps.Agent = sharedAgent
	deps.Log = log
	deps.SSOKey = ssoKeyPtr
	if sharedDB != nil {
		userRepo := repository.NewUserRepository(sharedDB)
		tokenRepo := repository.NewRefreshTokenRepository(sharedDB)
		packageRepo := repository.NewPackageRepository(sharedDB)
		domainRepo := repository.NewDomainRepository(sharedDB)
		dnsZoneRepo := repository.NewDNSZoneRepository(sharedDB)
		dnsRecordRepo := repository.NewDNSRecordRepository(sharedDB)
		sslCertRepo := repository.NewSSLCertificateRepository(sharedDB)
		databaseRepo := repository.NewDatabaseRepository(sharedDB)
		databaseUserRepo := repository.NewDatabaseUserRepository(sharedDB)
		databaseUserGrantRepo := repository.NewDatabaseUserGrantRepository(sharedDB)
		phpMyAdminSSOTokenRepo := repository.NewPhpMyAdminSSOTokenRepository(sharedDB)
		phpPoolRepo := repository.NewPHPPoolRepository(sharedDB)
		phpPoolIniOverrideRepo := repository.NewPHPPoolIniOverrideRepository(sharedDB)

		serverSettingsRepo := repository.NewServerSettingsRepository(sharedDB)
		jwtIss, err := auth.NewJWTIssuer(auth.JWTConfig{
			Secret:    []byte(cfg.Auth.JWTSecret),
			Issuer:    jwtIssuerName,
			KeyID:     jwtKeyID,
			AccessTTL: cfg.Auth.AccessTTL,
		})
		if err != nil {
			return err
		}

		authSvc := auth.NewService(auth.ServiceConfig{
			Users:       userRepo,
			RefreshRepo: tokenRepo,
			JWT:         jwtIss,
			BcryptCost:  bcrypt.DefaultCost,
			RefreshTTL:  cfg.Auth.RefreshTTL,
		})
		deps.Auth = authSvc
		deps.JWTIssuer = jwtIss
		deps.Users = userRepo
		deps.Packages = packageRepo
		deps.Domains = domainRepo

		deps.ServerSettings = serverSettingsRepo
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
		deps.Reconciler = rec
		deps.DNSZones = dnsZoneRepo
		deps.DNSRecords = dnsRecordRepo
		deps.SSLCerts = sslCertRepo
		deps.Databases = databaseRepo
		deps.DatabaseUsers = databaseUserRepo
		deps.DatabaseUserGrants = databaseUserGrantRepo
		deps.PhpMyAdminSSOTokens = phpMyAdminSSOTokenRepo
		deps.PHPPools = phpPoolRepo
		deps.PHPPoolIniOverrides = phpPoolIniOverrideRepo

		// Admin bootstrap.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		res, err := auth.BootstrapAdmin(ctx, userRepo, auth.BootstrapOptions{
			Email:      os.Getenv("JABALI_BOOTSTRAP_ADMIN_EMAIL"),
			Password:   os.Getenv("JABALI_BOOTSTRAP_ADMIN_PASSWORD"),
			BcryptCost: bcrypt.DefaultCost,
		})
		cancel()
		switch {
		case err != nil:
			return err
		case res.Created:
			log.Warn("admin user created via bootstrap")
		case res.ExistingID != "":
			log.Info("admin bootstrap: already exists", "user_id", res.ExistingID)
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
		}()
	}

	// ---- HTTP(S) ----
	handler := app.NewWithDeps(cfg, deps)
	useTLS := cfg.Server.TLSCert != "" && cfg.Server.TLSKey != ""

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
	if deps.Reconciler != nil {
		go deps.Reconciler.Start(ctx)
	}

	serveErr := make(chan error, 1)
	if useTLS {
		log.Info("TLS enabled", "cert", cfg.Server.TLSCert, "key", cfg.Server.TLSKey)
		go func() { serveErr <- srv.ListenAndServeTLS(cfg.Server.TLSCert, cfg.Server.TLSKey) }()
		go startHTTPRedirect(cfg.Server.Addr, log)
	} else {
		log.Warn("TLS not configured — serving plain HTTP")
		go func() { serveErr <- srv.ListenAndServe() }()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serveErr:
		cancel() // Stop reconciler on any serve error
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case sig := <-stop:
		log.Info("shutdown signal", "signal", sig.String())
		cancel() // Stop reconciler
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	log.Info("jabali-panel stopped")
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
