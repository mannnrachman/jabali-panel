// `jabali system restore` — operator-facing CLI for bare-metal recovery.
// Walks the disaster-recovery flow end-to-end: stops jabali-panel
// (the agent must STAY UP — it owns the socket the dispatch lands on),
// calls system.restore against the supplied remote, then restarts
// jabali-panel.
//
// Usage:
//   jabali system restore \
//       --remote-url=<repo path or sftp:user@host:/path> \
//       --credentials-ref=<path to env file>             # optional
//       --extra-option='sftp.command=...'                # optional, repeatable
//       --password-file=<path>                           # default
//       --snapshot=<system_manifest snapshot ID, or 'latest'> \
//       --include-accounts \
//       --force
//
// --force is required because the restore overwrites a running panel.
// We refuse without it (CLI only — no UI surface in v1 per ADR-0075).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
)

func newSystemRestoreCmd() *cobra.Command {
	var (
		remoteURL    string
		credsRef     string
		extraOpts    []string
		passwordFile string
		snapshot     string
		includeAccts bool
		apply        bool
		force        bool
	)
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a system backup onto this host (CLI only — see ADR-0080)",
		Long: `Restore a system backup onto a clean OS install. The bare-metal
recovery flow: fresh OS → bash <(install.sh --restore-from=...) →
jabali system restore --snapshot=latest --force.

Stops jabali-panel.service before dispatching, restarts it after.
jabali-agent.service stays up — its socket is the dispatch target.

Refuses without --force on a live host because the restore overwrites
the running panel.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !force {
				return errors.New("system restore requires --force; refusing on a running panel")
			}
			if snapshot == "" {
				snapshot = "latest"
			}
			if passwordFile == "" {
				passwordFile = "/etc/jabali-panel/restic-repo.password"
			}

			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
			defer cancel()

			// Stop panel — agent must keep running (it serves the
			// socket we're about to dispatch to).
			fmt.Fprintln(cmd.OutOrStdout(), "stopping jabali-panel.service…")
			_ = exec.CommandContext(ctx, "systemctl", "stop", "jabali-panel.service").Run()

			defer func() {
				fmt.Fprintln(cmd.OutOrStdout(), "starting jabali-panel.service…")
				_ = exec.CommandContext(context.Background(),
					"systemctl", "start", "jabali-panel.service").Run()
			}()

			ag := agent.NewClient(agent.Config{})
			jobID := ids.NewULID()
			params := map[string]any{
				"job_id":               jobID,
				"manifest_snapshot_id": snapshot,
				"include_accounts":     includeAccts,
				"repo_url":             remoteURL,
				"credentials_ref":      credsRef,
				"extra_options":        extraOpts,
				"apply":                apply,
			}
			fmt.Fprintf(cmd.OutOrStdout(), "→ agent system.restore job=%s snapshot=%s repo=%s\n",
				jobID, snapshot, remoteURL)
			raw, err := ag.Call(ctx, "system.restore", params)
			if err != nil {
				return fmt.Errorf("agent system.restore: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "✓ agent dispatch returned:")
			pretty, _ := json.MarshalIndent(json.RawMessage(raw), "  ", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), "  "+string(pretty))
			fmt.Fprintln(cmd.OutOrStdout(),
				"NOTE: stages restored to /var/lib/jabali-backups/restore-staging/<job-id>/.")
			fmt.Fprintln(cmd.OutOrStdout(),
				"      Operator must apply panel_db SQL + sync /etc/jabali-panel manually.")
			return nil
		},
	}
	cmd.Flags().StringVar(&remoteURL, "remote-url", "", "restic repo URL or local path (e.g. sftp:user@host:/path or /var/lib/jabali-backups/repo)")
	cmd.Flags().StringVar(&credsRef, "credentials-ref", "", "absolute path to env file with backend creds (root:root 0600)")
	cmd.Flags().StringSliceVar(&extraOpts, "extra-option", nil, "restic -o KEY=VALUE flag body (repeatable; e.g. 'sftp.command=ssh -p 2222 ...')")
	cmd.Flags().StringVar(&passwordFile, "password-file", "", "restic password file (default: /etc/jabali-panel/restic-repo.password)")
	cmd.Flags().StringVar(&snapshot, "snapshot", "latest", "system_manifest snapshot ID, or 'latest' to auto-pick newest")
	cmd.Flags().BoolVar(&includeAccts, "include-accounts", false, "also restore each linked account")
	cmd.Flags().BoolVar(&apply, "apply", true, "after staging, apply panel_db SQL + sync /etc/jabali-panel + /etc/letsencrypt onto live host (default true; --no-apply for inspect-only)")
	cmd.Flags().BoolVar(&force, "force", false, "required — restore overwrites the running panel")
	return cmd
}
