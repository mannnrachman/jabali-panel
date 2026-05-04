// `jabali backup account-restore` — operator-facing CLI to restore one
// account's snapshot directly via the agent. Bypasses the panel-api
// HTTP path so it works even when the panel UI is offline. Mirrors
// the parameter shape that POST /admin/backups/restore now sends.
//
// Resolves destination → repo_url + creds + sftp{} from /etc/jabali-
// panel using the same destination row the UI sees. Default behaviour
// is full apply (rsync home + mariadb load); pass --apply=false for
// staging-only smoke tests.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func newBackupAccountRestoreCmd() *cobra.Command {
	var (
		username       string
		userID         string
		targetUserID   string
		targetUsername string
		snapshotID     string
		destName       string
		applyFlag      bool
		force          bool
	)
	cmd := &cobra.Command{
		Use:   "account-restore",
		Short: "Restore one account's backup snapshot via the agent (bypass UI)",
		Long: `End-to-end account restore from the CLI. Resolves the destination
the snapshot lives on, dispatches backup.restore, prints the per-stage
result + applied list + warnings.

Examples:
  # Restore latest snapshot for shukivaknin from the 'test' destination
  jabali backup account-restore --user shukivaknin --destination test \
      --snapshot latest --force

  # Recon mode: materialize to staging, do not apply
  jabali backup account-restore --user shukivaknin --destination test \
      --snapshot latest --force --apply=false`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !force {
				return errors.New("account-restore requires --force; refusing on a live host")
			}
			if snapshotID == "" {
				return errors.New("--snapshot required (manifest snapshot id, or 'latest')")
			}
			if destName == "" {
				return errors.New("--destination required (matches backup_destinations.name)")
			}
			if err := initConfig(); err != nil {
				return err
			}
			if err := initDB(); err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			// Two resolution modes:
			//   1. Panel-managed: --user / --user-id resolve the panel
			//      row; restore reuses its ID + username.
			//   2. Disaster recovery: --target-user-id + --target-username
			//      bypass the panel row entirely. Useful when the panel
			//      DB lost the row (rebuilt host) but the system account
			//      was recreated by hand. Both flags must be set.
			var resolvedID, resolvedName string
			switch {
			case targetUserID != "" && targetUsername != "":
				resolvedID = targetUserID
				resolvedName = targetUsername
			case username != "" || userID != "":
				users := repository.NewUserRepository(sharedDB)
				if userID != "" {
					u, err := users.FindByID(ctx, userID)
					if err != nil || u == nil {
						return fmt.Errorf("user lookup by id %q: %w (use --target-user-id+--target-username for disaster recovery)", userID, err)
					}
					resolvedID = u.ID
					if u.Username != nil {
						resolvedName = *u.Username
					}
				} else {
					u, err := users.FindByUsername(ctx, username)
					if err != nil || u == nil {
						return fmt.Errorf("user lookup by username %q: %w (use --target-user-id+--target-username for disaster recovery)", username, err)
					}
					resolvedID = u.ID
					if u.Username != nil {
						resolvedName = *u.Username
					}
				}
				if resolvedName == "" {
					return fmt.Errorf("user %q has NULL username (admin user?) — restore needs a Linux account", resolvedID)
				}
			default:
				return errors.New("need --user OR --user-id OR (--target-user-id + --target-username) for disaster recovery")
			}

			dests := repository.NewBackupDestinationRepository(sharedDB)
			all, err := dests.ListEnabled(ctx)
			if err != nil {
				return fmt.Errorf("list destinations: %w", err)
			}
			var dest = (*struct {
				URL            string
				CredentialsRef *string
				Kind           string
			})(nil)
			_ = dest
			var pickedURL, pickedKind string
			var pickedCreds string
			var pickedID string
			for _, d := range all {
				if d.Name == destName {
					pickedURL = d.URL
					pickedKind = d.Kind
					pickedID = d.ID
					if d.CredentialsRef != nil {
						pickedCreds = *d.CredentialsRef
					}
					break
				}
			}
			if pickedURL == "" {
				names := make([]string, 0, len(all))
				for _, d := range all {
					names = append(names, d.Name)
				}
				return fmt.Errorf("destination %q not found; available: %v", destName, names)
			}

			// Resolve "latest" → newest manifest snapshot for this user
			// on this destination. Defer to caller for now: require
			// explicit snapshot id. (Adding latest-resolution requires
			// listing snapshots which is itself an agent round-trip.)
			if snapshotID == "latest" {
				return errors.New("--snapshot=latest not yet supported in CLI; pass the explicit manifest snapshot id")
			}

			jobID := ids.NewULID()
			params := map[string]any{
				"job_id":               jobID,
				"manifest_snapshot_id": snapshotID,
				"target_user_id":       resolvedID,
				"target_username":      resolvedName,
				"overwrite":            true,
				"apply_staged":         applyFlag,
				"repo_url":             pickedURL,
				"destination_kind":     pickedKind,
			}
			if pickedCreds != "" {
				params["credentials_ref"] = pickedCreds
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"→ backup.restore job=%s user=%s snapshot=%s dest=%s(%s) apply=%v\n",
				jobID, resolvedName, snapshotID, destName, pickedID, applyFlag)

			ag := agent.NewClient(agent.Config{Timeout: 1 * time.Hour})
			callCtx, callCancel := context.WithTimeout(ctx, 1*time.Hour)
			defer callCancel()
			raw, err := ag.Call(callCtx, "backup.restore", params)
			if err != nil {
				return fmt.Errorf("agent backup.restore: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "✓ agent returned:")
			pretty, _ := json.MarshalIndent(json.RawMessage(raw), "  ", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), "  "+string(pretty))
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "user", "", "username (e.g. shukivaknin) — looks up panel row")
	cmd.Flags().StringVar(&userID, "user-id", "", "user ULID — looks up panel row (alternative to --user)")
	cmd.Flags().StringVar(&targetUserID, "target-user-id", "", "disaster recovery: panel row gone; use this ULID directly (pair with --target-username)")
	cmd.Flags().StringVar(&targetUsername, "target-username", "", "disaster recovery: system account name to chown into (pair with --target-user-id)")
	cmd.Flags().StringVar(&snapshotID, "snapshot", "", "manifest snapshot id (long form preferred)")
	cmd.Flags().StringVar(&destName, "destination", "", "destination name (e.g. 'test', 'b2-prod')")
	cmd.Flags().BoolVar(&applyFlag, "apply", true, "apply home+db onto live system (false = staging-only smoke test)")
	cmd.Flags().BoolVar(&force, "force", false, "required — restore overwrites home tree + reloads databases")
	return cmd
}

// initConfig + initDB live in serve.go / shared cmd helpers.
var _ = os.Getenv // keep imports honest
