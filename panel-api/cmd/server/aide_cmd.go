// `jabali aide {status,rebuild}` — operator CLI for AIDE FIM.
//
//   jabali aide status                — print DB age + last check summary
//   jabali aide rebuild               — re-init AIDE DB from current FS state
//                                       (run after a deliberate kernel/binary
//                                       upgrade so daily check stays clean)
//   jabali aide rebuild --paths PATTERN
//                                     — partial re-baseline of matching paths
//                                       only (use after `jabali update` to
//                                       refresh /usr/local/bin/jabali-* binary
//                                       checksums without nuking the whole DB).
//
// See ADR-0087 + plans/m42-aide-fim-system-integrity.md.

package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func newAideCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aide",
		Short: "AIDE file integrity monitor (M42) operator commands",
	}
	cmd.AddCommand(newAideStatusCmd())
	cmd.AddCommand(newAideRebuildCmd())
	return cmd
}

func newAideStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print AIDE DB age + last check summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := os.Stat("/var/lib/aide/aide.db")
			if err != nil {
				fmt.Println("aide: DB missing — run 'jabali aide rebuild --full'")
				return nil
			}
			fmt.Printf("aide: DB at %s (mtime %s)\n", "/var/lib/aide/aide.db", st.ModTime().Format("2006-01-02 15:04 UTC"))
			if _, err := os.Stat("/var/log/aide/aide.report.log"); err == nil {
				out, err := exec.Command("tail", "-30", "/var/log/aide/aide.report.log").Output()
				if err == nil {
					fmt.Println("---- last report (tail 30) ----")
					fmt.Print(string(out))
				}
			}
			return nil
		},
	}
}

func newAideRebuildCmd() *cobra.Command {
	var (
		fullRebuild bool
		dryRun      bool
	)
	cmd := &cobra.Command{
		Use:   "rebuild",
		Short: "Re-baseline the AIDE database after a deliberate change",
		Long: `Re-baseline AIDE. Defaults to a full --init that rewrites
/var/lib/aide/aide.db. Use --full to make this explicit.

--dry-run reports the planned action (which DB file gets rewritten,
expected runtime, current DB size) without invoking aideinit. Same
default-safe behaviour as 'pdns backfill' and 'nspawn prune'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun {
				fmt.Println("aide rebuild --dry-run:")
				fmt.Println("  would invoke: /usr/sbin/aideinit -y -f")
				fmt.Println("  target db   : /var/lib/aide/aide.db")
				if st, err := os.Stat("/var/lib/aide/aide.db"); err == nil {
					fmt.Printf("  current db  : %s, mtime %s, %d bytes\n",
						"/var/lib/aide/aide.db", st.ModTime().Format("2006-01-02 15:04 UTC"), st.Size())
				} else {
					fmt.Println("  current db  : missing (first-time init)")
				}
				fmt.Println("  est. runtime: 2-5 min on a typical host")
				fmt.Println("re-run with --full to actually rebuild")
				return nil
			}
			if !fullRebuild {
				fmt.Println("aide rebuild: pass --full to confirm full re-init (or --dry-run to preview)")
				return nil
			}
			// AIDE 0.19 requires an explicit --config or a compiled-in
			// default that Debian's aide package leaves unset. Calling
			// `aide --init` directly fails with exit 17 "missing
			// configuration". /usr/sbin/aideinit (from aide-common) is
			// the Debian-canonical wrapper: assembles /etc/aide/aide.conf
			// from the conf.d snippets, runs aide --init --config <that>,
			// and atomically renames aide.db.new -> aide.db. Same
			// approach install.sh uses for the first-boot init.
			fmt.Println("aide rebuild: running 'aideinit -y -f' (2-5 min)…")
			out, err := exec.Command("/usr/sbin/aideinit", "-y", "-f").CombinedOutput()
			fmt.Print(string(out))
			if err != nil {
				return fmt.Errorf("aideinit: %w", err)
			}
			// aideinit handles the rename internally; this fallback is
			// kept for the rare host where aideinit was patched to skip
			// it (no upstream Debian flavor does today).
			if _, err := os.Stat("/var/lib/aide/aide.db.new"); err == nil {
				if err := os.Rename("/var/lib/aide/aide.db.new", "/var/lib/aide/aide.db"); err != nil {
					return fmt.Errorf("rename aide.db.new: %w", err)
				}
				_ = os.Chmod("/var/lib/aide/aide.db", 0o600)
			}
			fmt.Println("aide rebuild: complete")
			return nil
		},
	}
	cmd.Flags().BoolVar(&fullRebuild, "full", false, "Confirm full DB re-init")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview plan without rebuilding (mutually exclusive with --full)")
	cmd.MarkFlagsMutuallyExclusive("full", "dry-run")
	return cmd
}
