// `jabali migrate imapsync` cobra subcommand. Mail-only migration
// from any IMAP source to the destination Stalwart account using
// the imapsync binary on the panel host.
//
// Operator workflow:
//   1. Pre-create destination jabali user (with mail-enabled domain)
//   2. INSERT migration_jobs row with source_kind='imap_only'
//   3. apt install imapsync (one-time, panel host)
//   4. jabali migrate imapsync --job-id <id> \
//        --src-host imap.source.com --src-user u@source.com \
//        --src-password '<srcpw>' --dest-email u@dest.com \
//        --dest-password '<destpw>'
//
// Auto-marks the job state through validating → restoring → done
// (analyze + fix_perms are no-ops for imap_only). On failure
// transitions to failed with the imapsync stderr captured in
// last_error.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func newMigrateImapsyncCmd() *cobra.Command {
	var jobID, srcHost, srcUser, srcPassword string
	var srcPort int
	var srcNoSSL bool
	var destEmail, destPassword string
	cmd := &cobra.Command{
		Use:     "imapsync",
		Short:   "Sync mail from any IMAP source into destination Stalwart (M35 imap_only)",
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jobID == "" || srcHost == "" || srcUser == "" || srcPassword == "" || destEmail == "" || destPassword == "" {
				return errors.New("--job-id, --src-host, --src-user, --src-password, --dest-email, --dest-password all required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 6*time.Hour)
			defer cancel()
			repo := repository.NewMigrationJobRepository(sharedDB)
			job, err := repo.FindByID(ctx, jobID)
			if err != nil {
				return fmt.Errorf("load job: %w", err)
			}
			if job.SourceKind != models.MigrationSourceIMAPOnly {
				return fmt.Errorf("job source_kind=%q; this command only handles imap_only", job.SourceKind)
			}

			// Advance job state through analyzing/validating to
			// restoring. Pre-imap_only-runner shape — direct
			// state stamps without the migrate.Runner because
			// imap_only's pipeline is a single agent call.
			for _, st := range []string{models.MigrationStateAnalyzing, models.MigrationStateValidating, models.MigrationStateRestoring} {
				if !migrate.IsValidJobTransition(job.State, st) {
					return fmt.Errorf("illegal transition from %s → %s", job.State, st)
				}
				if err := repo.UpdateState(ctx, job.ID, st, nil); err != nil {
					return fmt.Errorf("update job state to %s: %w", st, err)
				}
				job.State = st
			}

			ssl := !srcNoSSL
			params := map[string]any{
				"job_id": job.ID,
				"src": map[string]any{
					"host":     srcHost,
					"port":     srcPort,
					"user":     srcUser,
					"password": srcPassword,
					"ssl":      ssl,
				},
				"dest": map[string]any{
					"email":    destEmail,
					"password": destPassword,
				},
			}
			raw, err := sharedAgent.Call(ctx, "migration.imapsync", params)
			if err != nil {
				emsg := err.Error()
				_ = repo.UpdateState(context.Background(), job.ID, models.MigrationStateFailed, &emsg)
				_ = migrate.WipeJobSecret(job.ID)
				return fmt.Errorf("agent.migration.imapsync: %w", err)
			}
			var resp struct {
				MessagesTransferred int64 `json:"messages_transferred"`
				BytesTransferred    int64 `json:"bytes_transferred"`
				DurationSeconds     int64 `json:"duration_seconds"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("decode imapsync result: %w", err)
			}
			summary := fmt.Sprintf("imapsync: messages=%d bytes=%d duration=%ds",
				resp.MessagesTransferred, resp.BytesTransferred, resp.DurationSeconds)
			_ = repo.UpdateManifest(ctx, job.ID, summary)
			if err := repo.UpdateState(ctx, job.ID, models.MigrationStateDone, nil); err != nil {
				return fmt.Errorf("final state update: %w", err)
			}
			_ = migrate.WipeJobSecret(job.ID)
			fmt.Fprintln(cmd.OutOrStdout(), summary)
			return nil
		},
	}
	cmd.Flags().StringVar(&jobID, "job-id", "", "migration_jobs.id (ULID) — required")
	cmd.Flags().StringVar(&srcHost, "src-host", "", "Source IMAP host (required)")
	cmd.Flags().IntVar(&srcPort, "src-port", 993, "Source IMAP port")
	cmd.Flags().StringVar(&srcUser, "src-user", "", "Source IMAP user (required)")
	cmd.Flags().StringVar(&srcPassword, "src-password", "", "Source IMAP password (required)")
	cmd.Flags().BoolVar(&srcNoSSL, "src-no-ssl", false, "Source IMAP without TLS (legacy port 143)")
	cmd.Flags().StringVar(&destEmail, "dest-email", "", "Destination Stalwart email (required; mail must already be enabled on the destination domain)")
	cmd.Flags().StringVar(&destPassword, "dest-password", "", "Destination user password (required)")
	return cmd
}
