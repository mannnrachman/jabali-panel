// `jabali migrate pull-source` cobra subcommand. Reads the per-job
// secrets file at /etc/jabali-panel/migration-secrets/<job-id>.env,
// connects to the source via SSH, runs the source-kind appropriate
// backup command (pkgacct / system_backup_user / v-backup-user),
// pulls the produced tarball back to /var/lib/jabali-migrations/
// <job-id>/, and extracts it under .../extracted/.
//
// Closes the operator workflow gap: previously the operator had to
// hand-run pkgacct + scp + tar -xzf before `jabali migrate import`
// could find an extracted tree. Now one cobra invocation handles
// all three.
//
// Operator workflow:
//   1. INSERT migration_jobs row (or via admin SPA drawer)
//   2. echo SSH_PASSWORD=… > /etc/jabali-panel/migration-secrets/<id>.env
//      (or SSH_PRIVATE_KEY=…)
//   3. jabali migrate pull-source --job-id <id>
//   4. jabali migrate import --job-id <id> --target-user … …
//
// WHM-pkgacct skipped — that source-kind is offline by design
// (operator-uploaded tarball, no live source). Returns an error
// directing the operator at scp.
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate/cpanel"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate/directadmin"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate/hestiacp"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func newMigratePullSourceCmd() *cobra.Command {
	var jobID string
	var sshUser string
	cmd := &cobra.Command{
		Use:   "pull-source",
		Short: "Connect to source via SSH, run kind-appropriate backup, pull + extract tarball",
		Long: `Reads the per-job secrets at
/etc/jabali-panel/migration-secrets/<job-id>.env then connects to
the source host (job.source_host) and runs the source-kind backup
command (pkgacct / system_backup_user / v-backup-user). Pulls the
produced tarball into /var/lib/jabali-migrations/<job-id>/ and
extracts under .../extracted/.

WHM-pkgacct is offline by design — operator-uploaded tarball, no
live source SSH. Use scp directly for that kind.`,
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jobID == "" {
				return errors.New("--job-id is required")
			}
			if sshUser == "" {
				sshUser = "root"
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 90*time.Minute)
			defer cancel()

			repo := repository.NewMigrationJobRepository(sharedDB)
			job, err := repo.FindByID(ctx, jobID)
			if err != nil {
				return fmt.Errorf("load job: %w", err)
			}
			if job.SourceHost == "" {
				return errors.New("job.source_host is empty — pull-source needs a live SSH target")
			}

			secretPath := fmt.Sprintf("%s/%s.env", migrate.SecretsDir, jobID)
			if _, err := os.Stat(secretPath); err != nil {
				return fmt.Errorf("secrets file %s missing: %w (drop SSH_PASSWORD or SSH_PRIVATE_KEY there first)", secretPath, err)
			}
			secret := migrate.SecretRef{Path: secretPath}

			// Local destination paths
			localDir := filepath.Join("/var/lib/jabali-migrations", jobID)
			if err := os.MkdirAll(localDir, 0o750); err != nil {
				return fmt.Errorf("mkdir %s: %w", localDir, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"connecting to %s@%s (kind=%s)...\n",
				sshUser, job.SourceHost, job.SourceKind)

			var localTar string
			switch job.SourceKind {
			case models.MigrationSourceCpanel:
				localTar, err = pullCpanel(ctx, sshUser, job, secret, localDir)
			case models.MigrationSourceDirectAdmin:
				localTar, err = pullDirectAdmin(ctx, sshUser, job, secret, localDir)
			case models.MigrationSourceHestia:
				localTar, err = pullHestia(ctx, sshUser, job, secret, localDir)
			case models.MigrationSourceWHMpkgacct:
				return errors.New("source kind whm_pkgacct is offline — scp the cpmove tarball into " + localDir + " manually")
			default:
				return fmt.Errorf("unknown source kind %q", job.SourceKind)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "tarball pulled: %s\n", localTar)

			// Extract.
			extractDir := filepath.Join(localDir, "extracted")
			if err := os.MkdirAll(extractDir, 0o750); err != nil {
				return fmt.Errorf("mkdir extract dir: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "extracting to %s...\n", extractDir)
			if err := extractTar(localTar, extractDir); err != nil {
				return fmt.Errorf("extract: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"done. next step: jabali migrate import --job-id %s --target-user <username>\n", jobID)
			return nil
		},
	}
	cmd.Flags().StringVar(&jobID, "job-id", "", "migration_jobs.id (ULID) — required")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "root", "SSH login on the source (default 'root')")
	return cmd
}

func pullCpanel(ctx context.Context, sshUser string, job *models.MigrationJob, secret migrate.SecretRef, localDir string) (string, error) {
	d := cpanel.New()
	s, err := d.Connect(ctx, job.SourceHost, sshUser, secret)
	if err != nil {
		return "", fmt.Errorf("cpanel.Connect: %w", err)
	}
	defer func() { _ = d.Close(ctx, s) }()
	remoteTar, err := d.Pkgacct(ctx, s, job.SourceUser)
	if err != nil {
		return "", fmt.Errorf("pkgacct: %w", err)
	}
	localTar := filepath.Join(localDir, fmt.Sprintf("cpmove-%s.tar.gz", job.SourceUser))
	if _, err := d.PullFile(ctx, s, remoteTar, localTar); err != nil {
		return "", fmt.Errorf("PullFile: %w", err)
	}
	return localTar, nil
}

func pullDirectAdmin(ctx context.Context, sshUser string, job *models.MigrationJob, secret migrate.SecretRef, localDir string) (string, error) {
	d := directadmin.New()
	s, err := d.Connect(ctx, job.SourceHost, sshUser, secret)
	if err != nil {
		return "", fmt.Errorf("directadmin.Connect: %w", err)
	}
	defer func() { _ = d.Close(ctx, s) }()
	remoteTar, err := d.BackupUser(ctx, s, job.SourceUser)
	if err != nil {
		return "", fmt.Errorf("system_backup_user: %w", err)
	}
	localTar := filepath.Join(localDir, fmt.Sprintf("user.%s.tar.gz", job.SourceUser))
	if _, err := d.PullFile(ctx, s, remoteTar, localTar); err != nil {
		return "", fmt.Errorf("PullFile: %w", err)
	}
	return localTar, nil
}

func pullHestia(ctx context.Context, sshUser string, job *models.MigrationJob, secret migrate.SecretRef, localDir string) (string, error) {
	d := hestiacp.New()
	s, err := d.Connect(ctx, job.SourceHost, sshUser, secret)
	if err != nil {
		return "", fmt.Errorf("hestiacp.Connect: %w", err)
	}
	defer func() { _ = d.Close(ctx, s) }()
	remoteTar, err := d.BackupUser(ctx, s, job.SourceUser)
	if err != nil {
		return "", fmt.Errorf("v-backup-user: %w", err)
	}
	// Use the remote filename's basename so .tar vs .tar.gz preserved.
	base := filepath.Base(remoteTar)
	if base == "" {
		base = fmt.Sprintf("%s.tar", job.SourceUser)
	}
	localTar := filepath.Join(localDir, base)
	if _, err := d.PullFile(ctx, s, remoteTar, localTar); err != nil {
		return "", fmt.Errorf("PullFile: %w", err)
	}
	return localTar, nil
}

// extractTar streams a .tar or .tar.gz into dest. Uses the same
// path-escape + size-cap hardening as cpanel.ParseTarball; doesn't
// classify entries since the per-importer parser does that.
func extractTar(tarPath, dest string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, 2)
	if _, err := io.ReadFull(f, buf); err != nil {
		return fmt.Errorf("magic: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var src io.Reader = f
	if buf[0] == 0x1f && buf[1] == 0x8b {
		gz, gerr := gzip.NewReader(f)
		if gerr != nil {
			return fmt.Errorf("gunzip: %w", gerr)
		}
		defer gz.Close()
		src = gz
	}
	tr := tar.NewReader(src)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || filepath.IsAbs(clean) {
			continue
		}
		out := filepath.Join(dest, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(out, 0o750); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
				return err
			}
			w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, io.LimitReader(tr, 100<<30)); err != nil {
				_ = w.Close()
				return err
			}
			if err := w.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}
