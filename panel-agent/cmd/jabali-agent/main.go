// Command jabali-agent is the root-privileged daemon that executes
// privileged host operations on behalf of panel-api. It listens on a Unix
// socket (default /run/jabali/agent.sock) and reads one NDJSON request per
// connection, dispatches to a handler from the commands registry, and
// writes a single NDJSON response.
//
// Access control is enforced entirely via socket permissions — agent never
// parses credentials. Production install places the socket in a directory
// owned root:jabali 0750 with the socket itself root:jabali 0660, so only
// root and the jabali group (i.e. the panel-api process) can connect.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/commands"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/pdns"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/server"
)

// Build-time metadata. Production builds pass:
//
//	-ldflags "-X main.version=<sha> -X main.commit=<sha> -X main.date=<iso>"
//
// Dev builds get "dev" and that's fine — the agent.version command reports
// whatever's baked in so upgrade-mismatch is detectable from the panel.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

const (
	defaultSocketPath = "/run/jabali/agent.sock"
	defaultTimeout    = 120 * time.Second
)

func main() {
	var (
		socketPath = flag.String("socket", envOr("JABALI_AGENT_SOCKET", defaultSocketPath), "path to the unix socket to listen on")
		socketGID  = flag.Int("gid", envInt("JABALI_AGENT_GID", -1), "chown socket to root:<gid> after bind; -1 to skip")
		timeout    = flag.Duration("timeout", defaultTimeout, "per-request wall-clock timeout (when caller sets no deadline)")
		logFormat  = flag.String("log-format", envOr("JABALI_AGENT_LOG_FORMAT", "json"), "json|text")
		logLevel   = flag.String("log-level", envOr("JABALI_AGENT_LOG_LEVEL", "info"), "debug|info|warn|error")
	)
	flag.Parse()

	log := newLogger(*logFormat, *logLevel)
	slog.SetDefault(log)

	// Populate the agent.version handler's metadata now that flags are parsed.
	commands.Version = version
	commands.StartTime = time.Now()

	log.Info("jabali-agent starting",
		"version", version, "commit", commit, "build_date", date,
		"socket", *socketPath, "gid", *socketGID, "timeout", timeout.String(),
	)

	// Ensure the socket directory exists with restrictive perms. install.sh
	// already creates /run/jabali but agents may be started out of systemd
	// (e.g. manual test) where the dir isn't there yet.
	if err := os.MkdirAll(filepath.Dir(*socketPath), 0750); err != nil {
		log.Error("mkdir socket dir failed", "err", err)
		os.Exit(2)
	}

	// Initialize PowerDNS backend client. Non-fatal if unavailable — dev boxes
	// may not have PowerDNS installed. Handlers will return a friendly error.
	if cl, err := pdns.ReadEnvAndConnect(); err != nil {
		log.Warn("pdns backend not available; dns.* commands will error", "err", err)
	} else {
		pdns.SetDefault(cl)
		log.Info("pdns backend connected")
		// Note: we hold the client for the process lifetime; no defer Close().
	}

	srv, err := server.New(server.Config{
		SocketPath:        *socketPath,
		SocketMode:        0660,
		SocketOwnerGID:    *socketGID,
		PerRequestTimeout: *timeout,
		Logger:            log,
	})
	if err != nil {
		log.Error("agent server init failed", "err", err)
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// M33 ingest path is now driven by maldet 2.0.1 post_scan_hook
	// (see /etc/jabali/maldet/post-scan-hook.sh). The earlier 5s
	// sessionwatcher poll was removed once the hook contract landed.

	if err := srv.Serve(ctx); err != nil {
		log.Error("agent serve failed", "err", err)
		os.Exit(1)
	}
	log.Info("jabali-agent stopped")
}

// newLogger builds a slog.Logger using the same format / level conventions
// as panel-api, so log aggregation sees a consistent shape across binaries.
func newLogger(format, level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if format == "text" {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}

// envOr returns the env var if set + non-empty, else fallback. Tiny helper
// so the flag defaults can pull from env without a third-party dep.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
