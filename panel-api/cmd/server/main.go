// Command server is the entry point for the Jabali Panel API.
//
// Phase 1: starts a minimal Gin server with only /health wired. Config,
// database, auth, agent RPC and the user surface come in later phases.
package main

import (
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/app"
)

// addrPattern accepts :PORT or HOST:PORT with a limited charset.
// Keeps gosec happy (no taint via env) and catches operator typos early.
var addrPattern = regexp.MustCompile(`^([A-Za-z0-9.\-]+)?:[0-9]{1,5}$`)

const (
	defaultAddr       = ":8443"
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 90 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	addr := os.Getenv("PANEL_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	if !addrPattern.MatchString(addr) {
		slog.Error("invalid PANEL_ADDR", "value", addr)
		os.Exit(2)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           app.New(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	slog.Info("starting jabali-panel", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
