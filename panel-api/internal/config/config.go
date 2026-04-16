// Package config loads, validates, and exposes panel runtime configuration.
//
// Precedence, highest wins:
//  1. environment variables (PANEL_*, LOG_*, DATABASE_URL, JWT_*, AGENT_*,
//     CORS_ALLOWED_ORIGINS).
//  2. TOML file passed to Load (usually /etc/jabali/config.toml).
//  3. Built-in defaults (Defaults()).
//
// A missing config file is not an error — defaults + env are enough to boot.
// Invalid TOML, invalid values, and missing-in-production secrets all fail
// fast with a descriptive error.
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Exported environment names, referenced by callers that want to branch on
// server.env (e.g. tighter logging or stricter error messages in prod).
const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
)

// Config is the root configuration struct. Sections map to TOML tables and to
// grouped env-var prefixes.
type Config struct {
	Server   ServerConfig   `toml:"server"`
	Log      LogConfig      `toml:"log"`
	Database DatabaseConfig `toml:"database"`
	Auth     AuthConfig     `toml:"auth"`
	Agent    AgentConfig    `toml:"agent"`
	CORS     CORSConfig     `toml:"cors"`
}

// ServerConfig controls HTTP listener and runtime mode.
type ServerConfig struct {
	// Addr is the listen address in host:port form.
	Addr string `toml:"addr"`
	// Env is "development" or "production". Affects log format defaults
	// and strictness of Validate.
	Env string `toml:"env"`

	// TLSCert and TLSKey are paths to the certificate and private key files.
	// When both are set, the server uses ListenAndServeTLS. When empty, it
	// falls back to plain HTTP (dev mode or behind a TLS-terminating proxy).
	TLSCert string `toml:"tls_cert"`
	TLSKey  string `toml:"tls_key"`

	// Server identity and DNS nameserver configuration for hosted domains.
	// Seeded from config.toml at first boot; thereafter edited via the
	// admin Settings API and stored in the server_settings DB table.
	Hostname   string `toml:"hostname"`
	PublicIPv4 string `toml:"public_ipv4"`
	PublicIPv6 string `toml:"public_ipv6"`
	NS1Name    string `toml:"ns1_name"`
	NS1IPv4    string `toml:"ns1_ipv4"`
	NS2Name    string `toml:"ns2_name"`
	NS2IPv4    string `toml:"ns2_ipv4"`
}

// LogConfig controls slog output.
type LogConfig struct {
	// Level: debug | info | warn | error.
	Level string `toml:"level"`
	// Format: text | json. Empty means "derive from Server.Env".
	Format string `toml:"format"`
}

// DatabaseConfig holds the MariaDB DSN for the panel's own schema.
// Customer databases live elsewhere; this is only the panel's control-plane DB.
type DatabaseConfig struct {
	// URL is a MariaDB DSN, e.g. "mysql://user:pass@tcp(host:3306)/jabali_panel?parseTime=true&charset=utf8mb4&loc=UTC"
	URL string `toml:"url"`
}

// AuthConfig holds JWT + refresh token settings.
type AuthConfig struct {
	JWTSecret  string        `toml:"jwt_secret"`
	AccessTTL  time.Duration `toml:"access_ttl"`
	RefreshTTL time.Duration `toml:"refresh_ttl"`

	// CookieSecure marks the refresh cookie Secure. When nil, defaults to
	// env=production. Only set to false in dev when serving over plain HTTP
	// without a TLS-terminating proxy in front; browsers + curl silently
	// drop Secure cookies received over http://.
	CookieSecure *bool `toml:"cookie_secure"`
}

// AgentConfig holds the Unix-socket path and per-call timeout for the
// jabali-agent daemon.
type AgentConfig struct {
	SocketPath         string        `toml:"socket_path"`
	Timeout            time.Duration `toml:"timeout"`
	ReconcilerInterval time.Duration `toml:"reconciler_interval"`
}

// CORSConfig holds the SPA origin whitelist.
type CORSConfig struct {
	AllowedOrigins []string `toml:"allowed_origins"`
}

// Defaults returns a Config populated with sensible development defaults.
// Required-in-production fields (JWT secret, database URL, agent socket)
// are intentionally blank; Validate() enforces them.
func Defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Addr: "127.0.0.1:8443",
			Env:  EnvDevelopment,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text", // auto-upgraded to json in production by Load()
		},
		Auth: AuthConfig{
			AccessTTL:  15 * time.Minute,
			RefreshTTL: 7 * 24 * time.Hour,
		},
		Agent: AgentConfig{
			SocketPath: "/run/jabali/agent.sock",
			// 30s: generous for most commands, short enough that a wedged
			// agent doesn't hold an HTTP request hostage for minutes.
			// Per-call ctx.Deadline() overrides this when tighter.
			Timeout:            30 * time.Second,
			ReconcilerInterval: 60 * time.Second,
		},
	}
}

// Load reads config from defaults, then (if present) the TOML file at
// tomlPath, then environment variables. An empty tomlPath skips the file
// step; a missing file is not an error.
func Load(tomlPath string) (*Config, error) {
	cfg := Defaults()

	if tomlPath != "" {
		if _, err := os.Stat(tomlPath); err == nil {
			if _, err := toml.DecodeFile(tomlPath, cfg); err != nil {
				return nil, fmt.Errorf("decode toml %q: %w", tomlPath, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat toml %q: %w", tomlPath, err)
		}
	}

	// Snapshot whether LOG_FORMAT was set via env BEFORE applying it, so we
	// can tell the difference between "user wants text in prod" and
	// "user hasn't picked a format, fall back to json".
	logFormatFromEnv := os.Getenv("LOG_FORMAT") != ""

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}

	// In production, if the operator did not pick a format via env, default
	// to JSON. An explicit `format = "text"` in TOML is currently still
	// upgraded — set LOG_FORMAT=text to force plain text in production.
	if cfg.Server.Env == EnvProduction && !logFormatFromEnv && cfg.Log.Format == "text" {
		cfg.Log.Format = "json"
	}

	return cfg, nil
}

// applyEnv overlays env-var values onto cfg. Only non-empty env values apply,
// so operators can leave variables unset to inherit from file/defaults.
func applyEnv(cfg *Config) error {
	if v := os.Getenv("PANEL_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	if v := os.Getenv("PANEL_ENV"); v != "" {
		cfg.Server.Env = v
	}
	if v := os.Getenv("TLS_CERT"); v != "" {
		cfg.Server.TLSCert = v
	}
	if v := os.Getenv("TLS_KEY"); v != "" {
		cfg.Server.TLSKey = v
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("LOG_FORMAT"); v != "" {
		cfg.Log.Format = v
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.Database.URL = v
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}
	if v := os.Getenv("JWT_ACCESS_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("JWT_ACCESS_TTL: %w", err)
		}
		cfg.Auth.AccessTTL = d
	}
	if v := os.Getenv("JWT_REFRESH_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("JWT_REFRESH_TTL: %w", err)
		}
		cfg.Auth.RefreshTTL = d
	}
	if v := os.Getenv("AUTH_COOKIE_SECURE"); v != "" {
		b := v == "true" || v == "1"
		cfg.Auth.CookieSecure = &b
	}
	if v := os.Getenv("AGENT_SOCKET"); v != "" {
		cfg.Agent.SocketPath = v
	}
	if v := os.Getenv("AGENT_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("AGENT_TIMEOUT: %w", err)
		}
		cfg.Agent.Timeout = d
	}
	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		cfg.CORS.AllowedOrigins = splitAndTrim(v, ",")
	}
	return nil
}

// addrPattern accepts :PORT or HOST:PORT with a limited charset.
var addrPattern = regexp.MustCompile(`^([A-Za-z0-9.\-]+)?:[0-9]{1,5}$`)

// Validate returns nil when cfg is usable; otherwise an error naming the
// specific field that's wrong. Production is strictly validated; development
// is permissive so contributors can boot without a full config.
func (c *Config) Validate() error {
	if !addrPattern.MatchString(c.Server.Addr) {
		return fmt.Errorf("server.addr %q: expected [host]:port", c.Server.Addr)
	}
	if c.Server.Env != EnvDevelopment && c.Server.Env != EnvProduction {
		return fmt.Errorf("server.env %q: must be %s or %s",
			c.Server.Env, EnvDevelopment, EnvProduction)
	}

	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log.level %q: must be one of debug|info|warn|error", c.Log.Level)
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		return fmt.Errorf("log.format %q: must be text|json", c.Log.Format)
	}

	// Production hardening: anything a live panel cannot boot without
	// is required here. Phase 2 doesn't use these yet — future phases
	// will; validating now catches misconfig at the earliest point.
	if c.Server.Env == "production" {
		if len(c.Auth.JWTSecret) < 32 {
			return errors.New("auth.jwt_secret: at least 32 bytes required in production")
		}
		if c.Database.URL == "" {
			return errors.New("database.url: required in production")
		}
	}
	return nil
}

// CookieSecureResolved returns the effective Secure flag for the refresh
// cookie. Explicit config wins; otherwise it mirrors env=production.
func (c *Config) CookieSecureResolved() bool {
	if c.Auth.CookieSecure != nil {
		return *c.Auth.CookieSecure
	}
	return c.Server.Env == EnvProduction
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
