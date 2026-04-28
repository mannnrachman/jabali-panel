// `jabali backup copy run --copy-job-id=<id>` — invoked by the
// systemd-run transient units the M30.1 copy worker spawns. Loads the
// copy_job row, finds its destination + creds env file, runs `restic
// copy`, then writes the terminal status back to the DB.
//
// Why a CLI subcommand and not just a goroutine in the worker? The
// systemd-run carry-over from M29: long-running subprocesses survive
// `jabali update` only if they're spawned as transient units. The
// worker decides WHICH copies to run; this CLI is the worker's hands.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internalbackup "git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/backupwrapperhelpers"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// resticCopyTimeout caps a single copy invocation. 4h covers ~50 GB at
// 30 Mb/s — much smaller than the system would tolerate before the
// next retention prune. Adjustable per-dest in M30.2 if needed.
const resticCopyTimeout = 4 * time.Hour

func newBackupCopyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "copy",
		Short: "Async restic copy of one backup snapshot to a destination (M30.1)",
	}
	cmd.AddCommand(newBackupCopyRunCmd())
	return cmd
}

func newBackupCopyRunCmd() *cobra.Command {
	var copyJobID string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute one copy_job (invoked by systemd-run from the copy worker)",
		Long: `Reads backup_copy_jobs.<id>, looks up the destination and credentials,
shells out to ` + "`restic copy`" + `, and writes the terminal row state back.

This command is normally invoked by systemd-run via the copy worker;
operators can run it manually for debugging when a transient unit
fails to spawn.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if copyJobID == "" {
				return errors.New("--copy-job-id is required")
			}
			ctx, cancel := context.WithTimeout(context.Background(), resticCopyTimeout)
			defer cancel()
			if err := initConfig(); err != nil {
				return err
			}
			if err := initDB(); err != nil {
				return err
			}
			return runBackupCopy(ctx, copyJobID, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&copyJobID, "copy-job-id", "", "ULID of the backup_copy_jobs row to execute")
	return cmd
}

func runBackupCopy(ctx context.Context, copyJobID string, stdout, stderr writer) error {
	copyRepo := repository.NewBackupCopyJobRepository(sharedDB)
	destRepo := repository.NewBackupDestinationRepository(sharedDB)
	jobRepo := repository.NewBackupJobRepository(sharedDB)

	cj, err := copyRepo.Get(ctx, copyJobID)
	if err != nil {
		return fmt.Errorf("load copy_job %s: %w", copyJobID, err)
	}
	dest, err := destRepo.Get(ctx, cj.DestinationID)
	if err != nil {
		return fmt.Errorf("load destination %s: %w", cj.DestinationID, err)
	}
	bj, err := jobRepo.Get(ctx, cj.BackupJobID)
	if err != nil {
		return fmt.Errorf("load backup_job %s: %w", cj.BackupJobID, err)
	}
	if dest.Kind == models.BackupDestinationKindLocal {
		return copyRepo.MarkSucceeded(ctx, cj.ID, 0)
	}
	if !dest.Enabled {
		return copyRepo.Cancel(ctx, cj.ID)
	}

	var extraEnv []string
	if dest.CredentialsRef != nil && *dest.CredentialsRef != "" {
		extraEnv, err = internalbackup.LoadEnvFile(*dest.CredentialsRef)
		if err != nil {
			markErr := copyRepo.MarkFailed(ctx, cj.ID, "load creds env: "+err.Error(), false, nil)
			if markErr != nil {
				fmt.Fprintf(stderr, "WARN mark-failed: %v\n", markErr)
			}
			return err
		}
	}

	if bj.SnapshotID == "" {
		// Backup hasn't sealed yet — the worker pulled too early.
		// Keep the row queued; the next tick re-tries.
		return copyRepo.MarkFailed(ctx, cj.ID, "backup snapshot not sealed yet", true, nil)
	}

	tag := internalbackup.Tag("job-id=" + bj.ID)
	fmt.Fprintf(stdout, "restic copy job=%s -> %s (%s)\n", bj.ID, dest.Name, dest.URL)

	out, errOut, copyErr := internalbackup.Copy(ctx, nil, internalbackup.CopyOpts{
		FromRepo:         internalbackup.DefaultRepo,
		FromPasswordFile: internalbackup.DefaultPasswordFile,
		ToRepo:           dest.URL,
		ToPasswordFile:   internalbackup.DefaultPasswordFile,
		Tags:             []internalbackup.Tag{tag},
		ExtraOptions:     backupwrapperhelpers.ResticOptionsFor(dest),
	}, extraEnv)
	if len(out) > 0 {
		fmt.Fprintln(stdout, strings.TrimRight(string(out), "\n"))
	}
	if len(errOut) > 0 {
		fmt.Fprintln(stderr, strings.TrimRight(string(errOut), "\n"))
	}
	if copyErr != nil {
		// Defer retry decision to the worker — this CLI runs inside a
		// transient unit; if we exit non-zero, the unit's exit code is
		// recorded but the worker ticks decide the retry.
		_ = copyRepo.MarkFailed(ctx, cj.ID, copyErr.Error(), retryAllowed(cj), nextAttempt(cj))
		return copyErr
	}

	// Best-effort byte count: parse the restic output for "Files: N new …"
	// or just leave 0. Until restic --json copy lands, we record 0 to
	// keep the schema honest rather than guess.
	return copyRepo.MarkSucceeded(ctx, cj.ID, 0)
}

func retryAllowed(cj *models.BackupCopyJob) bool {
	return cj.RetryCount+1 < models.BackupCopyJobMaxAttempts
}

func nextAttempt(cj *models.BackupCopyJob) *time.Time {
	idx := cj.RetryCount
	delays := []time.Duration{
		1 * time.Minute, 5 * time.Minute, 30 * time.Minute,
	}
	if idx >= len(delays) {
		return nil
	}
	t := time.Now().UTC().Add(delays[idx])
	return &t
}

// writer is the minimal io.Writer surface we want from cobra's
// OutOrStdout / ErrOrStderr. Avoids pulling all of io into this file.
type writer interface{ Write(p []byte) (int, error) }

// silence unused-import warning when the file is built with no
// transitive os.* references.
var _ = os.Stdout
