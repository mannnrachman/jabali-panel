package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/clientapi"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/db"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/logger"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

const defaultEnvFile = "/etc/jabali/panel.env"

// Shared state populated by initConfig / initDB / initAgent helpers.
// Subcommands call these in their own PreRunE so `jabali help` stays fast.
var (
	cfgPath    string
	jsonOutput bool

	sharedCfg   *config.Config
	sharedLog   *slog.Logger
	sharedDB    *gorm.DB
	sharedAgent *agent.Client
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jabali",
		Short: "Jabali Panel — web hosting control panel",
		Long:  "Jabali Panel CLI. Use subcommands to manage users, view system status, or start the panel server.",
		// Silence usage on error — cobra prints usage by default which is
		// noisy for operational commands.
		SilenceUsage: true,
	}

	cmd.PersistentFlags().StringVar(&cfgPath, "config", "", "config file path (default: /etc/jabali/config.toml)")
	cmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output as JSON")

	cmd.AddCommand(
		newServeCmd(),
		newUserCmd(),
		newPackageCmd(),
		newDomainCmd(),
		newSystemCmd(),
		newMigrateCmd(),
		newUpdateCmd(),
	)

	return cmd
}

// initConfig loads config + sets up the logger. Called by subcommands that
// need configuration. Idempotent — safe to call multiple times.
//
// When running outside systemd (CLI commands), env vars like DATABASE_URL
// aren't set. We auto-load /etc/jabali/panel.env if it exists so the CLI
// has the same config as the service without manual `source`.
func initConfig() error {
	if sharedCfg != nil {
		return nil
	}

	// Load the systemd env file so CLI commands see DATABASE_URL, JWT_SECRET, etc.
	loadEnvFile(defaultEnvFile)

	path := cfgPath
	if path == "" {
		path = os.Getenv("JABALI_CONFIG")
	}
	if path == "" {
		path = defaultConfigPath
	}

	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	sharedCfg = cfg
	sharedLog = logger.New(cfg.Log, os.Stdout)
	slog.SetDefault(sharedLog)
	return nil
}

// loadEnvFile reads a KEY=VALUE file (like systemd EnvironmentFile) and
// sets each pair via os.Setenv. Existing env vars are NOT overwritten so
// explicit exports still win. Silently skips if the file doesn't exist.
func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		// Don't overwrite — explicit env wins over file.
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}

// initDB opens the database. Requires initConfig first.
func initDB() error {
	if sharedDB != nil {
		return nil
	}
	if sharedCfg == nil {
		return fmt.Errorf("initDB: config not loaded")
	}
	if sharedCfg.Database.URL == "" || sharedCfg.Database.URL == "placeholder-until-phase-3" {
		return fmt.Errorf("DATABASE_URL not configured")
	}

	gdb, err := db.Open(db.Options{
		DSN:    sharedCfg.Database.URL,
		Silent: sharedCfg.Server.Env == config.EnvProduction,
	})
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	if err := db.Ping(gdb); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}
	sharedDB = gdb
	return nil
}

// initAgent sets up the agent client. Requires initConfig first.
func initAgent() error {
	if sharedAgent != nil {
		return nil
	}
	if sharedCfg == nil {
		return fmt.Errorf("initAgent: config not loaded")
	}
	sharedAgent = agent.NewClient(agent.Config{
		SocketPath: sharedCfg.Agent.SocketPath,
		Timeout:    sharedCfg.Agent.Timeout,
	})
	return nil
}

// userRepo is a convenience that returns a UserRepository from the shared DB.
func userRepo() repository.UserRepository {
	return repository.NewUserRepository(sharedDB)
}

// packageRepo is a convenience that returns a PackageRepository from the shared DB.
func packageRepoFromDB() repository.PackageRepository {
	return repository.NewPackageRepository(sharedDB)
}

// domainRepoFromDB returns a DomainRepository from the shared DB.
func domainRepoFromDB() repository.DomainRepository {
	return repository.NewDomainRepository(sharedDB)
}

// requireConfig initializes config only (no DB or agent).
// Used by CLI commands that interact via HTTP API instead of direct DB access.
func requireConfig(cmd *cobra.Command, args []string) error {
	return initConfig()
}

// newAPIClient creates an HTTP API client with CLI authentication.
// Requires config to be loaded first via requireConfig.
func newAPIClient(ctx context.Context, cfg *config.Config, log *slog.Logger) (*clientapi.Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("newAPIClient: config not loaded")
	}

	// Mint a short-lived JWT token for CLI authentication
	token, err := mintCLIToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("mint cli token: %w", err)
	}

	// cfg.Server.Addr is typically ":8443" (bound to all interfaces). For
	// a client URL we need an explicit host — substitute 127.0.0.1 when
	// the host portion is empty or wildcard so the URL is valid.
	scheme := "http"
	if cfg.Server.TLSCert != "" && cfg.Server.TLSKey != "" {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(cfg.Server.Addr)
	if err != nil {
		return nil, fmt.Errorf("parse server addr %q: %w", cfg.Server.Addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	baseURL := fmt.Sprintf("%s://%s:%s", scheme, host, port)

	return clientapi.NewClient(baseURL, token), nil
}

func requireDBAndAgent(cmd *cobra.Command, args []string) error {
	if err := initConfig(); err != nil {
		return err
	}
	if err := initDB(); err != nil {
		return err
	}
	return initAgent()
}
