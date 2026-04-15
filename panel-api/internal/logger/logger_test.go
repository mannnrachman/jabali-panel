package logger_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/logger"
)

func TestNew_JSONFormatEmitsParseableJSON(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	cfg := config.LogConfig{Level: "debug", Format: "json"}

	log := logger.New(cfg, buf)
	log.Info("hello", "k", "v")

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	assert.Equal(t, "INFO", rec["level"])
	assert.Equal(t, "hello", rec["msg"])
	assert.Equal(t, "v", rec["k"])
}

func TestNew_TextFormatEmitsPlainText(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	cfg := config.LogConfig{Level: "info", Format: "text"}

	log := logger.New(cfg, buf)
	log.Info("hello", "k", "v")

	out := buf.String()
	assert.Contains(t, out, "msg=hello")
	assert.Contains(t, out, "k=v")
	assert.False(t, strings.HasPrefix(strings.TrimSpace(out), "{"),
		"text handler should not produce JSON")
}

func TestNew_LevelFilter(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	cfg := config.LogConfig{Level: "warn", Format: "text"}
	log := logger.New(cfg, buf)

	log.Debug("debug-msg")
	log.Info("info-msg")
	log.Warn("warn-msg")
	log.Error("error-msg")

	out := buf.String()
	assert.NotContains(t, out, "debug-msg")
	assert.NotContains(t, out, "info-msg")
	assert.Contains(t, out, "warn-msg")
	assert.Contains(t, out, "error-msg")
}

func TestParseLevel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want slog.Level
		ok   bool
	}{
		{"debug", slog.LevelDebug, true},
		{"info", slog.LevelInfo, true},
		{"warn", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"DEBUG", slog.LevelDebug, true}, // case insensitive
		{"shouty", slog.LevelInfo, false},
		{"", slog.LevelInfo, false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := logger.ParseLevel(tc.in)
			if tc.ok {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestNew_UnknownFormatFallsBackToText(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	cfg := config.LogConfig{Level: "info", Format: "not-a-format"}

	// Validate() should have rejected this, but New() must still be defensive
	// if called directly (tests, library users).
	log := logger.New(cfg, buf)
	log.Info("hello")
	assert.Contains(t, buf.String(), "msg=hello")
}
