package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// cpmoveUserRe extracts <user> from a cPanel cpmove / WHM pkgacct
// tarball filename. cPanel emits `cpmove-<user>.tar.gz`; pkgacct emits
// `cpmove-<user>.tar.gz` or `backup-<...>_<user>.tar.gz`. We only
// auto-derive from the canonical cpmove- form; anything else → operator
// passes --source-user explicitly.
var cpmoveUserRe = regexp.MustCompile(`^cpmove-(.+)\.tar\.gz$`)

// cpmoveSourceUser returns the cPanel account from a cpmove path, or ""
// if the basename isn't the canonical cpmove-<user>.tar.gz shape.
func cpmoveSourceUser(path string) string {
	m := cpmoveUserRe.FindStringSubmatch(filepath.Base(path))
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

// stageTarball places src at dst (the path `jabali migrate import`
// expects: /var/lib/jabali-migrations/<job>/cpmove-<user>.tar.gz).
// Hardlink first — instant + zero extra disk when src and the staging
// dir share a filesystem (the common case: both on / ). Falls back to
// a streaming copy across devices. Never double-buffers.
func stageTarball(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil // already staged (idempotent re-run)
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

// newMigrateRestoreCmd is the one-shot offline restore: create the
// migration_jobs row, stage the cpmove tarball where the importer
// expects it, then run the existing four-stage import pipeline — no
// UI, no manual /var/lib steps, no separate job-id.
func newMigrateRestoreCmd() *cobra.Command {
	var cpanel bool
	var file, restoreFile, sourceUser, sourceHost string
	var targetUser, targetEmail, targetPassword, targetPackageID string
	var keepStaging bool

	cmd := &cobra.Command{
		Use:   "restore",
		Short: "One-shot offline restore from a cpmove tarball (create job + stage + import)",
		Long: `Restore a cPanel cpmove / WHM pkgacct tarball you already have on
this server, in one command:

  jabali migrate restore --cpanel --file /path/cpmove-<user>.tar.gz

It creates the migration_jobs row, copies the tarball to the path the
importer expects, and runs the full analyze → fix_perms → validate →
restore pipeline (home, MySQL, mail, DNS/domains).

Source account is read from the filename (cpmove-<user>.tar.gz);
override with --source-user. Destination jabali user defaults to the
source account; pass --target-email + --target-password to auto-create
it (else pre-create via the admin UI / jabali user CLI). Re-run the
same command to resume a failed job.`,
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			if !cpanel {
				return errors.New("a source kind is required: pass --cpanel")
			}
			kind := models.MigrationSourceCpanel

			if file == "" {
				file = restoreFile
			}
			if file == "" {
				return errors.New("--file is required (path to the cpmove tarball)")
			}
			abs, err := filepath.Abs(file)
			if err != nil {
				return err
			}
			fi, err := os.Stat(abs)
			if err != nil {
				return fmt.Errorf("--file: %w", err)
			}
			if fi.IsDir() {
				return fmt.Errorf("--file %q is a directory", abs)
			}

			su := sourceUser
			if su == "" {
				su = cpmoveSourceUser(abs)
			}
			if su == "" {
				return fmt.Errorf("cannot derive source user from %q "+
					"(expected cpmove-<user>.tar.gz) — pass --source-user",
					filepath.Base(abs))
			}

			jobsRepo := repository.NewMigrationJobRepository(sharedDB)
			var jobID string
			if ex, _ := jobsRepo.FindBySource(ctx, kind, sourceHost, su); ex != nil {
				jobID = ex.ID
				fmt.Printf("  → reusing existing job %s (state=%s) for %s/%s\n",
					ex.ID, ex.State, kind, su)
			} else {
				row := &models.MigrationJob{
					ID:         ids.NewULID(),
					SourceKind: kind,
					SourceHost: sourceHost,
					SourceUser: su,
					State:      models.MigrationStatePending,
				}
				if err := jobsRepo.Create(ctx, row); err != nil {
					return fmt.Errorf("create migration job: %w", err)
				}
				jobID = row.ID
				fmt.Printf("  → created migration job %s (%s/%s)\n", jobID, kind, su)
			}

			staging := filepath.Join("/var/lib/jabali-migrations", jobID)
			if err := os.MkdirAll(staging, 0o750); err != nil {
				return fmt.Errorf("mkdir staging dir: %w", err)
			}
			dst := filepath.Join(staging, fmt.Sprintf("cpmove-%s.tar.gz", su))
			if err := stageTarball(abs, dst); err != nil {
				return fmt.Errorf("stage tarball: %w", err)
			}
			fmt.Printf("  → staged %s\n", dst)

			// Reuse the entire import pipeline verbatim — zero
			// duplication. newMigrateImportCmd is a standalone cobra
			// command; SetArgs + Execute runs its PreRunE
			// (requireDBAndAgent, idempotent) + RunE.
			imp := newMigrateImportCmd()
			impArgs := []string{"--job-id", jobID}
			if targetUser != "" {
				impArgs = append(impArgs, "--target-user", targetUser)
			}
			if targetEmail != "" {
				impArgs = append(impArgs, "--target-email", targetEmail)
			}
			if targetPassword != "" {
				impArgs = append(impArgs, "--target-password", targetPassword)
			}
			if targetPackageID != "" {
				impArgs = append(impArgs, "--target-package-id", targetPackageID)
			}
			if keepStaging {
				impArgs = append(impArgs, "--keep-staging")
			}
			imp.SetArgs(impArgs)
			imp.SetContext(ctx)
			fmt.Printf("  → running import pipeline (job %s)\n", jobID)
			return imp.Execute()
		},
	}

	cmd.Flags().BoolVar(&cpanel, "cpanel", false, "source is a cPanel cpmove / WHM pkgacct tarball")
	cmd.Flags().StringVar(&file, "file", "", "path to the cpmove tarball (cpmove-<user>.tar.gz) — required")
	cmd.Flags().StringVar(&restoreFile, "restore-file", "", "alias of --file")
	cmd.Flags().StringVar(&sourceUser, "source-user", "", "cPanel account (default: derived from the cpmove filename)")
	cmd.Flags().StringVar(&sourceHost, "source-host", "", "informational source host (offline restore leaves this empty)")
	cmd.Flags().StringVar(&targetUser, "target-user", "", "destination jabali username (default: the source account)")
	cmd.Flags().StringVar(&targetEmail, "target-email", "", "destination email (only used when auto-creating the user)")
	cmd.Flags().StringVar(&targetPassword, "target-password", "", "destination password (auto-create only; ≥10 chars)")
	cmd.Flags().StringVar(&targetPackageID, "target-package-id", "", "hosting package ULID (auto-create only)")
	cmd.Flags().BoolVar(&keepStaging, "keep-staging", false, "keep /var/lib/jabali-migrations/<job-id>/ after the run (debug)")
	return cmd
}
