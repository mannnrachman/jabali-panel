// `jabali backup retention apply` — fired by jabali-backup-retention.timer
// daily at 04:30. Reads server_settings.backup_keep_{daily,weekly,monthly}
// and runs `restic forget --tag jabali --prune` with those values.
//
// The command exits 0 even when no snapshots match the retention scope
// (a fresh install with no backups yet) — that's not a failure, just a
// no-op.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

const (
	resticRepoDefault   = "/var/lib/jabali-backups/repo"
	resticPasswordFile  = "/etc/jabali-panel/restic-repo.password"
	resticBlanketTag    = "jabali"
	resticForgetTimeout = 30 * time.Minute
)

// newBackupCmd is the umbrella `jabali backup …` cobra group. Step 1
// only registers the retention subtree; Steps 2+ extend with create /
// restore / list / status etc.
func newBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Backup & restore subcommands (M30 — restic-backed; ADR-0075)",
	}
	cmd.AddCommand(newBackupRetentionCmd())
	cmd.AddCommand(newBackupCopyCmd())
	return cmd
}

func newBackupRetentionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retention",
		Short: "Manage restic retention (forget + prune)",
	}
	cmd.AddCommand(newBackupRetentionApplyCmd())
	return cmd
}

func newBackupRetentionApplyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Run restic forget+prune with values from server_settings",
		Long: `Reads server_settings.backup_keep_daily / backup_keep_weekly /
backup_keep_monthly and executes:

    restic forget --tag jabali \
        --keep-daily=<N> --keep-weekly=<N> --keep-monthly=<N> --prune

Wired into systemd timer jabali-backup-retention.timer (daily 04:30) by
install_backup_foundation in install.sh. Safe to run by hand for ad-hoc
sweeps.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), resticForgetTimeout)
			defer cancel()
			if err := initConfig(); err != nil {
				return err
			}
			if err := initDB(); err != nil {
				return err
			}

			settings, err := loadServerSettingsForRetention(ctx, sharedDB)
			if err != nil {
				return fmt.Errorf("load server_settings: %w", err)
			}

			if err := assertResticEnvironment(); err != nil {
				return err
			}

			args := []string{
				"--repo", resticRepoDefault,
				"--password-file", resticPasswordFile,
				"forget",
				"--tag", resticBlanketTag,
				"--keep-daily", strconv.FormatUint(uint64(settings.BackupKeepDaily), 10),
				"--keep-weekly", strconv.FormatUint(uint64(settings.BackupKeepWeekly), 10),
				"--keep-monthly", strconv.FormatUint(uint64(settings.BackupKeepMonthly), 10),
				"--prune",
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"running: restic forget --keep-daily=%d --keep-weekly=%d --keep-monthly=%d --prune --tag jabali\n",
				settings.BackupKeepDaily, settings.BackupKeepWeekly, settings.BackupKeepMonthly)

			c := exec.CommandContext(ctx, "restic", args...)
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			if err := c.Run(); err != nil {
				return fmt.Errorf("restic forget: %w", err)
			}
			return nil
		},
	}
}

// loadServerSettingsForRetention pulls the singleton row. Falls back to
// the migration-default values if the row doesn't exist yet (fresh
// install before serve.go's seed runs); failing the timer because of a
// fresh-install ordering quirk would be a regression.
func loadServerSettingsForRetention(ctx context.Context, db *gorm.DB) (*models.ServerSettings, error) {
	var s models.ServerSettings
	if err := db.WithContext(ctx).First(&s, "id = ?", 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &models.ServerSettings{
				BackupKeepDaily:   7,
				BackupKeepWeekly:  4,
				BackupKeepMonthly: 6,
			}, nil
		}
		return nil, err
	}
	if s.BackupKeepDaily == 0 && s.BackupKeepWeekly == 0 && s.BackupKeepMonthly == 0 {
		// Pre-migration-000084 row, or a buggy update wiped them. Use
		// migration defaults so the timer still produces a sensible
		// schedule rather than `--keep-daily=0` (which would forget
		// every snapshot under the jabali tag).
		s.BackupKeepDaily = 7
		s.BackupKeepWeekly = 4
		s.BackupKeepMonthly = 6
	}
	return &s, nil
}

// assertResticEnvironment fails fast if the binary or password file is
// missing — install_backup_foundation should guarantee both, but the
// CLI runs as `jabali` user, and a botched chmod on a previous version
// is detectable here without dragging the operator through restic's own
// (less helpful) error message.
func assertResticEnvironment() error {
	if _, err := exec.LookPath("restic"); err != nil {
		return fmt.Errorf("restic not on PATH: %w (run install_backup_foundation in install.sh)", err)
	}
	pwFI, err := os.Stat(resticPasswordFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", resticPasswordFile, err)
	}
	if pwFI.Size() == 0 {
		return fmt.Errorf("%s is empty (regenerate via install_backup_foundation)", resticPasswordFile)
	}
	return nil
}

// Compile-time check that we haven't accidentally orphaned the repository
// import (Step 6 will pull it in for backup_jobs lookups; for now the
// import is intentional so the package builds cleanly when wired into
// `jabali backup` subcommands beyond `retention`).
var _ = repository.NewBackupJobRepository
