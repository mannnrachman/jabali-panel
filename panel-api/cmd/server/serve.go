package main

import (
	"context"
	"errors"
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
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
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

	// ---- auth + deps ----
	var deps app.Deps
	deps.Agent = sharedAgent
	if sharedDB != nil {
		userRepo := repository.NewUserRepository(sharedDB)
		tokenRepo := repository.NewRefreshTokenRepository(sharedDB)

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
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case sig := <-stop:
		log.Info("shutdown signal", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
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
