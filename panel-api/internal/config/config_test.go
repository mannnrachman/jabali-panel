package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
)

// withEnv sets the given env vars for the duration of a test and restores
// the previous state afterwards. Uses t.Setenv so tests must NOT use t.Parallel.
func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

// clearPanelEnv scrubs every env var the loader reads, so a test starts from a
// known-empty state. t.Setenv restores the original values after the test.
func clearPanelEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PANEL_ADDR",
		"PANEL_ENV",
		"LOG_LEVEL",
		"LOG_FORMAT",
		"DATABASE_URL",
		"JWT_SECRET",
		"JWT_ACCESS_TTL",
		"JWT_REFRESH_TTL",
		"AGENT_SOCKET",
		"AGENT_TIMEOUT",
		"CORS_ALLOWED_ORIGINS",
	} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearPanelEnv(t)

	cfg, err := config.Load("")
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1:8443", cfg.Server.Addr)
	assert.Equal(t, "development", cfg.Server.Env)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, "text", cfg.Log.Format) // dev default
	assert.Equal(t, 15*time.Minute, cfg.Auth.AccessTTL)
	assert.Equal(t, 7*24*time.Hour, cfg.Auth.RefreshTTL)
	assert.Equal(t, 5*time.Second, cfg.Agent.Timeout)
	assert.Empty(t, cfg.CORS.AllowedOrigins)
}

func TestLoad_ProductionFormatDefaultsToJSON(t *testing.T) {
	clearPanelEnv(t)
	withEnv(t, map[string]string{"PANEL_ENV": "production"})

	cfg, err := config.Load("")
	require.NoError(t, err)
	assert.Equal(t, "production", cfg.Server.Env)
	assert.Equal(t, "json", cfg.Log.Format)
}

func TestLoad_EnvOverridesDefaults(t *testing.T) {
	clearPanelEnv(t)
	withEnv(t, map[string]string{
		"PANEL_ADDR":           "0.0.0.0:9999",
		"LOG_LEVEL":            "debug",
		"LOG_FORMAT":           "json",
		"DATABASE_URL":         "mysql://u:p@tcp(db:3306)/jabali_panel?parseTime=true",
		"JWT_SECRET":           "01234567890123456789012345678901", // 32 bytes
		"JWT_ACCESS_TTL":       "30m",
		"JWT_REFRESH_TTL":      "48h",
		"AGENT_SOCKET":         "/run/jabali/agent.sock",
		"AGENT_TIMEOUT":        "10s",
		"CORS_ALLOWED_ORIGINS": "https://a.example,https://b.example",
	})

	cfg, err := config.Load("")
	require.NoError(t, err)
	assert.Equal(t, "0.0.0.0:9999", cfg.Server.Addr)
	assert.Equal(t, "debug", cfg.Log.Level)
	assert.Equal(t, "json", cfg.Log.Format)
	assert.Equal(t, "mysql://u:p@tcp(db:3306)/jabali_panel?parseTime=true", cfg.Database.URL)
	assert.Equal(t, "01234567890123456789012345678901", cfg.Auth.JWTSecret)
	assert.Equal(t, 30*time.Minute, cfg.Auth.AccessTTL)
	assert.Equal(t, 48*time.Hour, cfg.Auth.RefreshTTL)
	assert.Equal(t, "/run/jabali/agent.sock", cfg.Agent.SocketPath)
	assert.Equal(t, 10*time.Second, cfg.Agent.Timeout)
	assert.Equal(t, []string{"https://a.example", "https://b.example"}, cfg.CORS.AllowedOrigins)
}

func TestLoad_FileOverridesDefaults(t *testing.T) {
	clearPanelEnv(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[server]
addr = "10.0.0.1:8443"
env = "production"

[log]
level = "warn"

[auth]
access_ttl = "20m"

[agent]
socket_path = "/tmp/a.sock"
`), 0o600))

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1:8443", cfg.Server.Addr)
	assert.Equal(t, "production", cfg.Server.Env)
	assert.Equal(t, "warn", cfg.Log.Level)
	assert.Equal(t, "json", cfg.Log.Format) // derived from env=production
	assert.Equal(t, 20*time.Minute, cfg.Auth.AccessTTL)
	assert.Equal(t, "/tmp/a.sock", cfg.Agent.SocketPath)
}

func TestLoad_EnvWinsOverFile(t *testing.T) {
	clearPanelEnv(t)
	withEnv(t, map[string]string{"PANEL_ADDR": "1.2.3.4:5000"})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[server]
addr = "10.0.0.1:8443"
`), 0o600))

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "1.2.3.4:5000", cfg.Server.Addr)
}

func TestLoad_MissingFileIsNotAnError(t *testing.T) {
	clearPanelEnv(t)
	cfg, err := config.Load("/nonexistent/config.toml")
	require.NoError(t, err)
	assert.NotNil(t, cfg)
}

func TestLoad_InvalidTOMLFails(t *testing.T) {
	clearPanelEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	require.NoError(t, os.WriteFile(path, []byte("this = is = not toml"), 0o600))

	_, err := config.Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "toml")
}

func TestValidate_RejectsBadAddr(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Addr = "not-a-host-or-port"
	require.Error(t, cfg.Validate())
}

func TestValidate_RejectsBadLogLevel(t *testing.T) {
	cfg := config.Defaults()
	cfg.Log.Level = "shouty"
	require.Error(t, cfg.Validate())
}

func TestValidate_RejectsBadLogFormat(t *testing.T) {
	cfg := config.Defaults()
	cfg.Log.Format = "yaml"
	require.Error(t, cfg.Validate())
}

func TestValidate_RejectsShortJWTSecretInProd(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Env = "production"
	cfg.Auth.JWTSecret = "too-short"
	require.Error(t, cfg.Validate())
}

func TestValidate_RequiresDatabaseURLInProd(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Env = "production"
	cfg.Auth.JWTSecret = "01234567890123456789012345678901"
	cfg.Database.URL = ""
	require.Error(t, cfg.Validate())
}

func TestValidate_AllowsEmptyJWTSecretInDev(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Env = "development"
	// JWT secret empty, DB empty, agent empty — all fine in dev (Phase 2
	// doesn't use them yet; they become required as phases turn them on).
	require.NoError(t, cfg.Validate())
}
