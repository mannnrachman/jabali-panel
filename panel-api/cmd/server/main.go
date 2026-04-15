// Command server is the entry point for the Jabali Panel API.
//
// Phase 2: config + slog logger wired in front of the Gin engine. Future
// phases add DB, auth, agent RPC, and the full route surface.
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

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/app"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/logger"
)

const (
	defaultConfigPath = "/etc/jabali/config.toml"

	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 90 * time.Second
	shutdownTimeout   = 10 * time.Second
)

func main() {
	// Bootstrap logger so config-load errors are visible. Replaced once
	// real config is loaded.
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

	srv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           app.New(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	// Graceful shutdown on SIGTERM / SIGINT.
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
