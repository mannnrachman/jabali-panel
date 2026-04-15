// Command server is the entry point for the Jabali Panel API.
//
// Boot order:
//  1. Bootstrap logger (stderr, plain) so early errors are legible.
//  2. Load config from /etc/jabali/config.toml + env. Fail fast on bad shape.
//  3. Swap in the real slog logger.
//  4. Open DB, run pending migrations, ping.
//  5. Instantiate auth (JWT issuer + service) + wire into app.
//  6. Bootstrap an admin user if env vars request it + none exists.
//  7. Start HTTP listener. Graceful shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/app"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/db"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/logger"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

const (
	defaultConfigPath = "/etc/jabali/config.toml"

	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 90 * time.Second
	shutdownTimeout   = 10 * time.Second

	jwtIssuerName = "jabali-panel"
	jwtKeyID      = "v1"
)

func main() {
	bootLog := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfgPath := os.Getenv("JABALI_CONFIG")
	if cfgPath == "" {
		cfgPath = defaultConfigPath
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		bootLog.Error("config load failed", "path", cfgPath, "err", err)
		os.Exit(2)
	}
	if err := cfg.Validate(); err != nil {
		bootLog.Error("config validation failed", "err", err)
		os.Exit(2)
	}

	log := logger.New(cfg.Log, os.Stdout)
	slog.SetDefault(log)

	log.Info("starting jabali-panel",
		"addr", cfg.Server.Addr,
		"env", cfg.Server.Env,
		"log_level", cfg.Log.Level,
		"log_format", cfg.Log.Format,
		"config_path", cfgPath,
	)

	// ---- DB ----
	var gdb *gorm.DB
	if cfg.Database.URL != "" && cfg.Database.URL != "placeholder-until-phase-3" {
		if os.Getenv("SKIP_MIGRATIONS") != "true" {
			if err := db.Migrate(cfg.Database.URL); err != nil {
				log.Error("migrations failed", "err", err)
				os.Exit(3)
			}
			log.Info("migrations up-to-date")
		}
		gdb, err = db.Open(db.Options{
			DSN:    cfg.Database.URL,
			Silent: cfg.Server.Env == config.EnvProduction,
		})
		if err != nil {
			log.Error("db open failed", "err", err)
			os.Exit(3)
		}
		if err := db.Ping(gdb); err != nil {
			log.Error("db ping failed", "err", err)
			os.Exit(3)
		}
		log.Info("db connected")
	} else {
		log.Warn("DATABASE_URL not set (or placeholder); running without DB")
	}

	// ---- auth wiring (only if DB is up — no DB means no auth) ----
	var deps app.Deps
	if gdb != nil {
		userRepo := repository.NewUserRepository(gdb)
		tokenRepo := repository.NewRefreshTokenRepository(gdb)

		jwtIss, err := auth.NewJWTIssuer(auth.JWTConfig{
			Secret:    []byte(cfg.Auth.JWTSecret),
			Issuer:    jwtIssuerName,
			KeyID:     jwtKeyID,
			AccessTTL: cfg.Auth.AccessTTL,
		})
		if err != nil {
			log.Error("jwt issuer init failed", "err", err)
			os.Exit(3)
		}

		authSvc := auth.NewService(auth.ServiceConfig{
			Users:       userRepo,
			RefreshRepo: tokenRepo,
			JWT:         jwtIss,
			BcryptCost:  bcrypt.DefaultCost,
			RefreshTTL:  cfg.Auth.RefreshTTL,
		})
		deps.Auth = authSvc

		// Optional: one-shot admin bootstrap. Safe to leave the env vars
		// set — after the first run the function is a no-op (and never
		// overwrites an existing admin's password).
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		res, err := auth.BootstrapAdmin(ctx, userRepo, auth.BootstrapOptions{
			Email:      os.Getenv("JABALI_BOOTSTRAP_ADMIN_EMAIL"),
			Password:   os.Getenv("JABALI_BOOTSTRAP_ADMIN_PASSWORD"),
			BcryptCost: bcrypt.DefaultCost,
		})
		cancel()
		switch {
		case err != nil:
			log.Error("bootstrap admin failed", "err", err)
			os.Exit(3)
		case res.Created:
			log.Warn("admin user created via bootstrap — UNSET the JABALI_BOOTSTRAP_ADMIN_* env vars now")
		case res.ExistingID != "":
			log.Info("admin bootstrap: already exists", "user_id", res.ExistingID)
		case res.SkippedEmpty:
			// quiet — most common branch
		}
	}

	// ---- HTTP ----
	srv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           app.NewWithDeps(cfg, deps),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server exited", "err", err)
			os.Exit(1)
		}
	case sig := <-stop:
		log.Info("shutdown signal", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Error("graceful shutdown failed", "err", err)
			os.Exit(1)
		}
	}
	log.Info("jabali-panel stopped")
}
