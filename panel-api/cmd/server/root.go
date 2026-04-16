package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/db"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/logger"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

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
		newSystemCmd(),
		newMigrateCmd(),
	)

	return cmd
}

// initConfig loads config + sets up the logger. Called by subcommands that
// need configuration. Idempotent — safe to call multiple times.
func initConfig() error {
	if sharedCfg != nil {
		return nil
	}
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
