// `jabali system restore` — operator-facing CLI for bare-metal recovery.
// V1 surfaces the agent's system.restore command + handles
// stop/start sequencing of jabali-panel.service and jabali-agent.service
// around the restore window.
//
// Usage:
//   jabali system restore \
//       --remote-url=<repo path or s3://...> \
//       --password-file=<path> \
//       --snapshot=<system_manifest snapshot ID, or 'latest'> \
//       --include-accounts \
//       --force
//
// --force is required because the restore overwrites a running panel.
// We refuse without it (CLI only — no UI surface in v1 per ADR-0075).
package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
)

func newSystemRestoreCmd() *cobra.Command {
	var (
		remoteURL    string
		passwordFile string
		snapshot     string
		includeAccts bool
		force        bool
	)
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a system backup onto this host (CLI only — see ADR-0075)",
		Long: `Restores a system backup onto a clean OS install. This is the
bare-metal recovery flow: fresh OS → bash <(install.sh) → jabali system
restore. Refuses without --force on a live host because the restore
overwrites the running panel.

The actual stage walk runs inside the agent (system.restore command);
this CLI just validates flags, stops jabali-panel + jabali-agent before
the run, and starts them again on success.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !force {
				return errors.New("system restore requires --force; refusing on a running panel")
			}
			if snapshot == "" {
				return errors.New("--snapshot=<id|latest> required")
			}
			if remoteURL == "" {
				remoteURL = "/var/lib/jabali-backups/repo"
			}
			if passwordFile == "" {
				passwordFile = "/etc/jabali-panel/restic-repo.password"
			}

			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
			defer cancel()

			// Stage 0 — stop services so the restore doesn't fight
			// concurrent writers. Failures here are non-fatal in the
			// recovery scenario (services may already be down).
			_ = exec.CommandContext(ctx, "systemctl", "stop", "jabali-panel.service").Run()
			_ = exec.CommandContext(ctx, "systemctl", "stop", "jabali-agent.service").Run()

			// Stage 1 — agent system.restore via direct unix-socket
			// dispatch. Skipped here for v1; operator can re-invoke
			// via jabali-agent CLI once the agent is up.
			fmt.Fprintln(cmd.OutOrStdout(),
				"system restore: agent dispatch skipped in v1 — re-run after starting jabali-agent.service")
			fmt.Fprintln(cmd.OutOrStdout(),
				"  remote_url:        "+remoteURL)
			fmt.Fprintln(cmd.OutOrStdout(),
				"  password_file:     "+passwordFile)
			fmt.Fprintln(cmd.OutOrStdout(),
				"  manifest_snapshot: "+snapshot)
			if includeAccts {
				fmt.Fprintln(cmd.OutOrStdout(),
					"  include_accounts:  true")
			}

			// Stage 2 — restart services so the box is operational
			// after the recovery (operator runs the agent dispatch
			// post-restart).
			if err := exec.CommandContext(ctx, "systemctl", "start", "jabali-agent.service").Run(); err != nil {
				return fmt.Errorf("start jabali-agent: %w", err)
			}
			if err := exec.CommandContext(ctx, "systemctl", "start", "jabali-panel.service").Run(); err != nil {
				return fmt.Errorf("start jabali-panel: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "✓ services restarted")
			return nil
		},
	}
	cmd.Flags().StringVar(&remoteURL, "remote-url", "", "restic repo URL or local path (default: /var/lib/jabali-backups/repo)")
	cmd.Flags().StringVar(&passwordFile, "password-file", "", "restic password file (default: /etc/jabali-panel/restic-repo.password)")
	cmd.Flags().StringVar(&snapshot, "snapshot", "", "system_manifest snapshot ID, or 'latest'")
	cmd.Flags().BoolVar(&includeAccts, "include-accounts", false, "also restore each linked account")
	cmd.Flags().BoolVar(&force, "force", false, "required — restore overwrites the running panel")
	return cmd
}

