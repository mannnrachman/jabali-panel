// `jabali migrate reap-secrets` cobra subcommand. Walks
// migration_jobs WHERE state IN ('done','failed','cancelled') +
// deletes the per-job env file at /etc/jabali-panel/migration-
// secrets/<job-id>.env (if present).
//
// Closes the M35 ADR-0094 §"tracked risks" gap: per-job source
// credentials previously persisted across job-terminal state with
// no scheduled wipe. Reaper run by jabali-migration-secrets-reap.timer
// (daily 04:30 UTC + jitter), idempotent; missing files are
// non-fatal.
//
// Operator can run on demand via: jabali migrate reap-secrets
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

const migrationSecretsDir = "/etc/jabali-panel/migration-secrets"

func newMigrateReapSecretsCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "reap-secrets",
		Short: "Wipe per-job migration-secrets env files for terminal-state jobs",
		Long: `Walks migration_jobs WHERE state IN
('done','failed','cancelled') and deletes the matching env file
at /etc/jabali-panel/migration-secrets/<job-id>.env. Idempotent —
missing files don't fail. Run by jabali-migration-secrets-reap.timer
on a daily cadence; operator can also invoke directly.`,
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			repo := repository.NewMigrationJobRepository(sharedDB)
			pageSize := 200
			page := 1
			deleted := 0
			scanned := 0
			for {
				rows, total, err := repo.List(ctx, page, pageSize)
				if err != nil {
					return fmt.Errorf("list migration jobs: %w", err)
				}
				for _, row := range rows {
					scanned++
					if !isTerminal(row.State) {
						continue
					}
					path := filepath.Join(migrationSecretsDir, row.ID+".env")
					if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
						continue
					}
					if dryRun {
						fmt.Fprintf(cmd.OutOrStdout(), "[dry-run] would remove %s (job state=%s)\n", path, row.State)
						deleted++
						continue
					}
					if err := os.Remove(path); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "remove %s: %v\n", path, err)
						continue
					}
					deleted++
				}
				if int64(page*pageSize) >= total || len(rows) == 0 {
					break
				}
				page++
			}
			fmt.Fprintf(cmd.OutOrStdout(), "scanned=%d deleted=%d (dry-run=%v)\n", scanned, deleted, dryRun)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "List would-delete paths without removing them")
	return cmd
}

func isTerminal(state string) bool {
	switch state {
	case models.MigrationStateDone, models.MigrationStateFailed, models.MigrationStateCancelled:
		return true
	}
	return false
}
