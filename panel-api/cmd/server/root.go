package main

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/db"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/logger"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

const defaultEnvFile = "/etc/jabali/panel.env"

// cliVersionString feeds cobra's --version output. Reuses the link-time
// Version baked into panel-api/internal/api/health.go so HTTP /health
// and the CLI report the same string. Empty `Version=dev` is the
// untagged-build placeholder; install.sh sets it via -ldflags.
func cliVersionString() string {
	return api.Version
}

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
		Use:     "jabali",
		Version: cliVersionString(),
		Short:   "Jabali Panel — web hosting control panel",
		Long:    "Jabali Panel CLI. Use subcommands to manage users, view system status, or start the panel server.",
		// Usage is shown on error by default. This matters most when a
		// caller forgets a required flag (e.g. `jabali app install`):
		// Cobra emits "Error: required flag(s) not set" AND the command's
		// full usage, so the operator sees which flags to add without a
		// second invocation. Operational errors (server down, 500 from
		// panel) show usage too — slightly noisier, but the usage output
		// is never misleading, and for a CLI this size the extra context
		// is worth it. Individual commands can set SilenceUsage=true
		// locally if their error paths are already self-explanatory.
		SilenceUsage: false,
	}

	cmd.PersistentFlags().StringVar(&cfgPath, "config", "", "config file path (default: /etc/jabali/config.toml)")
	cmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output as JSON")

	// `jabali admin` is a grouping command for operator-only subcommands.
	// M5b (admin-login) and M5c (disable-2fa) were removed by M20 — 2FA
	// now lives in Kratos and is managed via `kratos identities patch
	// <id> --clear-totp`. Slice-cutover stays because it's filesystem
	// migration, not auth.
	adminCmd := &cobra.Command{
		Use:   "admin",
		Short: "Operator-only administrative subcommands",
	}
	adminCmd.AddCommand(newAdminSliceCutoverCmd())
	adminCmd.AddCommand(newAdminRebuildKratosCmd())

	cmd.AddCommand(
		newServeCmd(),
		newUserCmd(),
		newPackageCmd(),
		newDomainCmd(),
		newAppCmd(),
		newSystemCmd(),
		newMigrateCmd(),
		newUpdateCmd(),
		newLimitsCmd(),
		newMailboxCmd(),
		newPdnsCmd(),
		newPanelPrimaryCmd(),
		newNspawnCmd(),
		adminCmd,
		newSSOCmd(),
		newSSOReapCmd(),
		newMalwarePurgeCmd(),
		newBackupCmd(),
		newRepairCmd(),
		newPerUserEgressCmd(),
		newAppArmorCmd(),
		newAideCmd(),
		newUfwCmd(),
		newSSLCmd(),
		newSSHKeyCmd(),
		newPHPCmd(),
		newCronCmd(),
		newDBCmd(),
		newAuditCmd(),
	)
	// `jabali reconcile` was removed by M20 — the reconciler already ticks
	// every cfg.Agent.ReconcilerInterval (default 60s), and the CLI's
	// manual-trigger path relied on legacy JWT that the Kratos-era
	// middleware ignores. Operators who need an immediate tick can restart
	// jabali-panel (`systemctl restart jabali-panel`) which re-runs every
	// reconcile loop on boot.

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

// databaseRepoFromDB returns a DatabaseRepository from the shared DB.
func databaseRepoFromDB() repository.DatabaseRepository {
	return repository.NewDatabaseRepository(sharedDB)
}

// databaseUserRepoFromDB returns a DatabaseUserRepository from the shared DB.
func databaseUserRepoFromDB() repository.DatabaseUserRepository {
	return repository.NewDatabaseUserRepository(sharedDB)
}

// requireConfig initializes config only (no DB or agent).
// Used by CLI commands that only need to read config.
func requireConfig(cmd *cobra.Command, args []string) error {
	return initConfig()
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

func requireDB(cmd *cobra.Command, args []string) error {
	if err := initConfig(); err != nil {
		return err
	}
	return initDB()
}
