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

const (
	migrationSecretsDir = "/etc/jabali-panel/migration-secrets"
	migrationStagingDir = "/var/lib/jabali-migrations"
	// migrationStagingMaxAge — terminal jobs whose tarball + extracted
	// tree have been on disk longer than this get reaped. 7 days gives
	// the operator a generous window to download the cpmove for forensics
	// or re-attempt a manual fixup before disk goes away. Override via
	// `--staging-max-age` flag for one-shot operator runs.
	migrationStagingMaxAge = 7 * 24 * time.Hour
)

func newMigrateReapSecretsCmd() *cobra.Command {
	var dryRun bool
	var stagingMaxAge time.Duration
	cmd := &cobra.Command{
		Use:   "reap-secrets",
		Short: "Wipe per-job migration-secrets env files + stale tarball/extracted dirs",
		Long: `Walks migration_jobs WHERE state IN
('done','failed','cancelled') and deletes the matching env file
at /etc/jabali-panel/migration-secrets/<job-id>.env. Same pass
removes the per-job tarball + extracted tree under
/var/lib/jabali-migrations/<id>/ when the job's ended_at is older
than --staging-max-age (default 7 days). Idempotent — missing
files don't fail. Run by jabali-migration-secrets-reap.timer on a
daily cadence; operator can also invoke directly.`,
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
					if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
						if dryRun {
							fmt.Fprintf(cmd.OutOrStdout(), "[dry-run] would remove %s (job state=%s)\n", path, row.State)
							deleted++
						} else if err := os.Remove(path); err != nil {
							fmt.Fprintf(cmd.ErrOrStderr(), "remove %s: %v\n", path, err)
						} else {
							deleted++
						}
					}
					// Staging dir: tarball + extracted tree. Reap when
					// the job has been terminal long enough (ended_at +
					// stagingMaxAge < now). Operator can `--staging-max-age 0`
					// to wipe immediately.
					stagingPath := filepath.Join(migrationStagingDir, row.ID)
					if info, sErr := os.Stat(stagingPath); sErr == nil && info.IsDir() {
						ref := row.EndedAt
						if ref == nil || ref.IsZero() {
							ref = &row.UpdatedAt
						}
						age := time.Since(*ref)
						if age < stagingMaxAge {
							continue
						}
						if dryRun {
							fmt.Fprintf(cmd.OutOrStdout(), "[dry-run] would rm -rf %s (state=%s age=%s)\n", stagingPath, row.State, age.Truncate(time.Hour))
							deleted++
							continue
						}
						if rErr := os.RemoveAll(stagingPath); rErr != nil {
							fmt.Fprintf(cmd.ErrOrStderr(), "rm -rf %s: %v\n", stagingPath, rErr)
							continue
						}
						deleted++
					}
				}
				if int64(page*pageSize) >= total || len(rows) == 0 {
					break
				}
				page++
			}
			fmt.Fprintf(cmd.OutOrStdout(), "scanned=%d deleted=%d (dry-run=%v)\n", scanned, deleted, dryRun)
			// ADR-0095 decision 5 — also reap draft migration_jobs
			// older than 24h. Drafts are created by the wizard at Step
			// 1; an operator who closes the tab without finishing
			// leaves a row behind. 24h is a generous "not coming back"
			// window. Hard-delete (no per-job secrets to wipe; secrets
			// only land at Step 2 which flips state out of draft).
			cutoff := time.Now().Add(-24 * time.Hour)
			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "[dry-run] would reap draft jobs older than %s\n", cutoff.Format(time.RFC3339))
			} else {
				if n, err := repo.CancelDraftsOlderThan(ctx, cutoff); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "reap drafts: %v\n", err)
				} else if n > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "reaped %d draft job(s) older than 24h\n", n)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "List would-delete paths without removing them")
	cmd.Flags().DurationVar(&stagingMaxAge, "staging-max-age", migrationStagingMaxAge,
		"Reap /var/lib/jabali-migrations/<id>/ only when the job has been terminal at least this long (default 168h = 7d; pass 0 to wipe immediately)")
	return cmd
}

func isTerminal(state string) bool {
	switch state {
	case models.MigrationStateDone, models.MigrationStateFailed, models.MigrationStateCancelled:
		return true
	}
	return false
}
