// `jabali backup retention apply` — fired by jabali-backup-retention.timer
// daily at 04:30. Iterates every enabled backup_schedules row and runs
// `restic forget --tag schedule-id=<id>` with that schedule's
// keep_{daily,weekly,monthly} values, then a single `restic prune` at
// the end so blob removal happens once per timer fire instead of N
// times.
//
// Manual (non-scheduled) backups carry no schedule-id tag and are
// therefore NEVER auto-pruned. Operators who want them gone delete
// them by hand with `restic forget --tag job-id=<id>`.
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

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

const (
	resticRepoDefault   = "/var/lib/jabali-backups/repo"
	resticPasswordFile  = "/etc/jabali-panel/restic-repo.password"
	resticForgetTimeout = 30 * time.Minute
)

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
		Short: "Run restic forget per schedule + a final prune",
		Long: `For every enabled backup_schedules row with non-null keep_{daily,
weekly,monthly}, run:

    restic forget --tag schedule-id=<id> \
        --keep-daily=<N> --keep-weekly=<N> --keep-monthly=<N>

then a single ` + "`restic prune`" + ` at the end. Schedules with all-NULL
keep_* are skipped (the operator hasn't picked a policy yet). Manual
backups (ScheduleID NULL) are never pruned.

Wired into systemd timer jabali-backup-retention.timer (daily 04:30)
by install_backup_foundation in install.sh.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), resticForgetTimeout)
			defer cancel()
			if err := initConfig(); err != nil {
				return err
			}
			if err := initDB(); err != nil {
				return err
			}
			if err := assertResticEnvironment(); err != nil {
				return err
			}

			schedRepo := repository.NewBackupScheduleRepository(sharedDB)
			scheds, err := schedRepo.List(ctx)
			if err != nil {
				return fmt.Errorf("list backup_schedules: %w", err)
			}

			anyForgotten := false
			for _, s := range scheds {
				if !s.Enabled {
					continue
				}
				if s.KeepDaily == nil && s.KeepWeekly == nil && s.KeepMonthly == nil {
					fmt.Fprintf(cmd.OutOrStdout(),
						"schedule %s: no retention policy, skipping\n", s.ID)
					continue
				}
				if err := forgetForSchedule(ctx, cmd, s); err != nil {
					// Don't abort — one bad schedule must not block the
					// rest of the sweep.
					fmt.Fprintf(cmd.ErrOrStderr(),
						"schedule %s forget failed: %v\n", s.ID, err)
					continue
				}
				anyForgotten = true
			}

			if !anyForgotten {
				fmt.Fprintln(cmd.OutOrStdout(),
					"no schedules with retention policy; nothing to forget or prune")
				return nil
			}

			fmt.Fprintln(cmd.OutOrStdout(), "running: restic prune")
			pruneCmd := exec.CommandContext(ctx, "restic",
				"--repo", resticRepoDefault,
				"--password-file", resticPasswordFile,
				"prune",
			)
			pruneCmd.Stdout = cmd.OutOrStdout()
			pruneCmd.Stderr = cmd.ErrOrStderr()
			if err := pruneCmd.Run(); err != nil {
				return fmt.Errorf("restic prune: %w", err)
			}
			return nil
		},
	}
}

func forgetForSchedule(ctx context.Context, cmd *cobra.Command, s models.BackupSchedule) error {
	args := []string{
		"--repo", resticRepoDefault,
		"--password-file", resticPasswordFile,
		"forget",
		"--tag", "schedule-id=" + s.ID,
	}
	if s.KeepDaily != nil {
		args = append(args, "--keep-daily", strconv.Itoa(*s.KeepDaily))
	}
	if s.KeepWeekly != nil {
		args = append(args, "--keep-weekly", strconv.Itoa(*s.KeepWeekly))
	}
	if s.KeepMonthly != nil {
		args = append(args, "--keep-monthly", strconv.Itoa(*s.KeepMonthly))
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"schedule %s: restic forget --tag schedule-id=%s daily=%s weekly=%s monthly=%s\n",
		s.ID, s.ID,
		intPtrStr(s.KeepDaily), intPtrStr(s.KeepWeekly), intPtrStr(s.KeepMonthly))
	c := exec.CommandContext(ctx, "restic", args...)
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	return c.Run()
}

func intPtrStr(p *int) string {
	if p == nil {
		return "-"
	}
	return strconv.Itoa(*p)
}

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

// Silence import if errors becomes unused.
var _ = errors.Is
