// Package logger builds a *slog.Logger from panel config.
//
// Callers pass a LogConfig and a writer (usually os.Stdout); the package
// picks a text or JSON handler, filters by level, and returns a ready logger.
// Request-scoped fields (request_id, user_id) are added at handler time in
// later phases via a middleware that puts values into context and uses
// slog.Logger.With.
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
)

// New returns a *slog.Logger configured per cfg, writing to w. Unknown formats
// fall back to text so a misconfigured config can never swallow logs silently.
func New(cfg config.LogConfig, w io.Writer) *slog.Logger {
	level, err := ParseLevel(cfg.Level)
	if err != nil {
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}

	var h slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "json":
		h = slog.NewJSONHandler(w, opts)
	default:
		// "text" + defensive fallback for anything else
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(h)
}

// ParseLevel converts a textual level into slog's Level type. Case-insensitive.
// An empty or unknown value returns an error; callers decide whether to
// substitute a default or propagate the error.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q", s)
	}
}
