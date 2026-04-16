package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/db"
)

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Database migration commands",
	}
	cmd.AddCommand(newMigrateUpCmd())
	return cmd
}

func newMigrateUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Run pending database migrations",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return initConfig()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := sharedCfg
			if cfg.Database.URL == "" || cfg.Database.URL == "placeholder-until-phase-3" {
				return fmt.Errorf("DATABASE_URL not configured")
			}

			if err := db.Migrate(cfg.Database.URL); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}

			fmt.Println("Migrations up-to-date.")
			return nil
		},
	}
}
