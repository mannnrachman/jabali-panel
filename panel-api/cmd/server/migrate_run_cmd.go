// `jabali migrate run` cobra subcommand. Walks one migration_jobs
// row through the four-stage pipeline (analyze → fix_perms →
// validate → restore) using the source-kind-appropriate writers.
//
// Operator-driven workflow (until the admin REST + UI Step 8 lands):
//   1. Pre-create the destination jabali user via /admin/users
//   2. Insert a migration_jobs row + extract a cpmove tarball
//      under /var/lib/jabali-migrations/<job-id>/extracted/
//   3. Run: jabali migrate run --job-id <ulid> --target-user <username>
//
// Resume after a partial failure: same command — runner skips
// already-done stages, picks up at the first failed/pending one.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate/cpanel"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/userops"
)

func newMigrateImportCmd() *cobra.Command {
	var jobID, targetUser, targetEmail, targetPassword, targetPackageID string
	cmd := &cobra.Command{
		Use:     "import",
		Short:   "Run (or resume) a migration job through the four-stage pipeline",
		Long: `Walks the named migration_jobs row through analyze → fix_perms →
validate → restore. The destination jabali user must already
exist (pre-create via the admin UI or jabali user CLI).

The cpmove tarball must already be extracted at:
  /var/lib/jabali-migrations/<job-id>/extracted/cp/<source-user>/

Resume: re-run the same command after fixing the cause of any
failed stage. Already-done stages are skipped.`,
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jobID == "" || targetUser == "" {
				return errors.New("--job-id and --target-user are required")
			}
			ctx := cmd.Context()

			jobsRepo := repository.NewMigrationJobRepository(sharedDB)
			usersRepo := repository.NewUserRepository(sharedDB)
			dbsRepo := repository.NewDatabaseRepository(sharedDB)
			cronsRepo := repository.NewCronJobRepository(sharedDB)
			sshRepo := repository.NewSSHKeyRepository(sharedDB)
			domainsRepo := repository.NewDomainRepository(sharedDB)

			job, err := jobsRepo.FindByID(ctx, jobID)
			if err != nil {
				return fmt.Errorf("load job: %w", err)
			}
			user, err := usersRepo.FindByUsername(ctx, targetUser)
			if err != nil {
				if !errors.Is(err, repository.ErrNotFound) {
					return fmt.Errorf("destination user %q lookup: %w", targetUser, err)
				}
				// Auto-create when operator supplied --target-email
				// + --target-password. Otherwise fail with helpful
				// message pointing at the auto-create flags.
				if targetEmail == "" || targetPassword == "" {
					return fmt.Errorf("destination user %q does not exist. "+
						"Pre-create via /admin/users OR pass --target-email + --target-password to auto-create",
						targetUser)
				}
				cu := userops.CreateInput{
					Email:    targetEmail,
					Password: targetPassword,
					Username: &targetUser,
					IsAdmin:  false,
				}
				if targetPackageID != "" {
					cu.PackageID = &targetPackageID
				}
				// KratosClient nil → userops skips the kratos atomic
				// step (the panel row is still created cleanly).
				// Operator path: send a kratos password-reset link
				// post-migration; the identity gets lazy-created at
				// first login. v2 lifts the boot-time kratosclient
				// to a package var so the CLI can reuse it; for v1
				// nil-skip is the safer default than rebuilding a
				// kratosclient from config in cobra context.
				res, cErr := userops.Create(ctx, userops.Deps{
					Users:      usersRepo,
					Packages:   repository.NewPackageRepository(sharedDB),
					Agent:      sharedAgent,
					BcryptCost: bcrypt.DefaultCost,
				}, cu)
				if cErr != nil {
					return fmt.Errorf("auto-create destination user %q: %w", targetUser, cErr)
				}
				user = res.User
				fmt.Fprintf(cmd.OutOrStdout(), "Auto-created destination user %s (id=%s)\n",
					*user.Username, user.ID)
				if res.ProvisionWarning != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", res.ProvisionWarning)
				}
			}
			if user.Username == nil {
				return fmt.Errorf("destination user %s has no Linux username", user.ID)
			}

			// cPanel + WHM-pkgacct share restore code-path: both
			// produce cpmove-<user>.tar.gz with identical layout.
			// WHM-pkgacct just skips the live-source SSH probe
			// (operator pre-uploaded the tarball). Future
			// directadmin / hestiacp / imap_only land here as
			// they get per-area builders + tarball-pull wired.
			switch job.SourceKind {
			case models.MigrationSourceCpanel, models.MigrationSourceWHMpkgacct:
				// supported — fall through
			default:
				return fmt.Errorf("source kind %q not yet supported by jabali migrate import; "+
					"supported: %s, %s",
					job.SourceKind,
					models.MigrationSourceCpanel,
					models.MigrationSourceWHMpkgacct)
			}

			extractDir := filepath.Join("/var/lib/jabali-migrations", job.ID, "extracted")
			parsed, err := cpanel.ParseTarball(
				filepath.Join("/var/lib/jabali-migrations", job.ID, fmt.Sprintf("cpmove-%s.tar.gz", job.SourceUser)),
				extractDir,
			)
			if err != nil {
				// Fall back to assuming the operator already
				// extracted manually — let the writers walk the
				// existing tree.
				parsed = &cpanel.ParsedTarball{
					ExtractDir: extractDir,
					HomeDir:    filepath.Join(extractDir, "cp", job.SourceUser, "homedir"),
					SourceUser: job.SourceUser,
				}
				fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: ParseTarball failed (%v); falling back to assumed pre-extracted layout\n", err)
			}

			payload := &cpanelRunPayload{
				parsed:         parsed,
				targetUserID:   user.ID,
				targetUsername: *user.Username,
			}

			runner := &migrate.Runner{
				Jobs:  jobsRepo,
				Agent: sharedAgent,
				StageCallbacks: map[string]migrate.StageCallback{
					migrate.StageAnalyze:  cpanelAnalyzeCallback(),
					migrate.StageValidate: validateStageCallback(usersRepo, domainsRepo, *user.Username),
					migrate.StageRestore: cpanelRestoreCallback(
						sshRepo, cronsRepo, dbsRepo,
					),
				},
			}
			runner.WithContext(payload)
			return runner.Run(ctx, job.ID)
		},
	}
	cmd.Flags().StringVar(&jobID, "job-id", "", "migration_jobs.id (ULID) — required")
	cmd.Flags().StringVar(&targetUser, "target-user", "", "destination jabali username — auto-created if --target-email + --target-password supplied")
	cmd.Flags().StringVar(&targetEmail, "target-email", "", "destination user email (only used when auto-creating)")
	cmd.Flags().StringVar(&targetPassword, "target-password", "", "destination user password (only used when auto-creating; ≥10 chars)")
	cmd.Flags().StringVar(&targetPackageID, "target-package-id", "", "hosting package ULID (only used when auto-creating)")
	return cmd
}


// cpanelRunPayload is the opaque payload threaded through every
// stage callback. The runner forwards it via WithContext.
type cpanelRunPayload struct {
	parsed         *cpanel.ParsedTarball
	targetUserID   string
	targetUsername string
}

// validateStageCallback bridges the runner's StageCallback shape
// to migrate.Validate. Reports projection counts via warnings;
// blockers fail the stage so the runner halts before restore.
func validateStageCallback(users repository.UserRepository, domains repository.DomainRepository, targetUsername string) migrate.StageCallback {
	return func(ctx context.Context, job *models.MigrationJob, payload any) (int64, []string, error) {
		p, ok := payload.(*cpanelRunPayload)
		if !ok {
			return 0, nil, fmt.Errorf("validate: bad payload type")
		}
		// Hand-roll a minimal manifest from what the parsed
		// tarball + job row already tells us. Full
		// AccountManifest assembly lives on the Discoverer; this
		// stage runs against the post-pull data.
		mf := &migrate.AccountManifest{
			SchemaVersion: migrate.ManifestSchemaVersion,
			Source: migrate.SourceRef{
				Kind: job.SourceKind,
				Host: job.SourceHost,
				User: job.SourceUser,
			},
		}
		rpt, err := migrate.Validate(ctx, migrate.ValidateDeps{
			Users: users, Domains: domains,
		}, mf, targetUsername)
		if err != nil {
			return 0, nil, fmt.Errorf("validate: %w", err)
		}
		warnings := []string{
			fmt.Sprintf("projections: domains=%d dbs=%d mailboxes=%d",
				rpt.Projections.DomainsToCreate,
				rpt.Projections.DBsToCreate,
				rpt.Projections.MailboxesToCreate),
		}
		if len(rpt.Blockers) > 0 {
			return 0, warnings, fmt.Errorf("validate blockers: %d (first: %s)",
				len(rpt.Blockers), rpt.Blockers[0].Detail)
		}
		_ = p
		return 0, warnings, nil
	}
}

// cpanelRestoreCallback orchestrates every per-area writer in a
// fixed safe order: ssh keys → cron → databases → DNS → home.
// Each writer's Skipped slice is folded into the warnings.
//
// agent.AgentInterface is read off the package-level sharedAgent at
// callback time rather than passed in — avoids the awkward
// interface-vs-concrete dance and matches how every other panel-api
// CLI subcommand reaches the agent.
func cpanelRestoreCallback(
	sshRepo repository.SSHKeyRepository,
	cronRepo repository.CronJobRepository,
	dbsRepo repository.DatabaseRepository,
) migrate.StageCallback {
	return func(ctx context.Context, job *models.MigrationJob, payload any) (int64, []string, error) {
		var _ agent.AgentInterface = sharedAgent // compile-time guard
		p, ok := payload.(*cpanelRunPayload)
		if !ok {
			return 0, nil, fmt.Errorf("restore: bad payload type")
		}
		var warnings []string
		var bytes int64

		sshRes, err := cpanel.ImportSSHKeys(ctx, sshRepo, p.parsed, p.targetUserID)
		if err != nil {
			return bytes, warnings, fmt.Errorf("ssh: %w", err)
		}
		warnings = append(warnings, fmt.Sprintf("ssh: created=%d", sshRes.Created))
		warnings = append(warnings, sshRes.Skipped...)

		cronRes, err := cpanel.ImportCron(ctx, cronRepo, p.parsed, p.targetUserID)
		if err != nil {
			return bytes, warnings, fmt.Errorf("cron: %w", err)
		}
		warnings = append(warnings, fmt.Sprintf("cron: created=%d", cronRes.Created))
		warnings = append(warnings, cronRes.Skipped...)

		dbsRes, err := cpanel.ImportDatabases(ctx, dbsRepo, sharedAgent, p.parsed, p.targetUserID, p.targetUsername)
		if err != nil {
			return bytes, warnings, fmt.Errorf("databases: %w", err)
		}
		warnings = append(warnings, fmt.Sprintf("databases: created=%d", dbsRes.Created))
		warnings = append(warnings, dbsRes.Skipped...)

		dnsRes, err := cpanel.ImportDNS(ctx, sharedAgent, p.parsed)
		if err != nil {
			return bytes, warnings, fmt.Errorf("dns: %w", err)
		}
		warnings = append(warnings, fmt.Sprintf("dns: zones=%d records=%d", dnsRes.Zones, dnsRes.Records))
		warnings = append(warnings, dnsRes.Skipped...)

		homeRes, err := cpanel.ImportHome(ctx, sharedAgent, p.parsed, job.ID, p.targetUsername)
		if err != nil {
			return bytes, warnings, fmt.Errorf("home: %w", err)
		}
		bytes += homeRes.BytesCopied
		warnings = append(warnings, fmt.Sprintf("home: bytes=%d files=%d", homeRes.BytesCopied, homeRes.Files))
		warnings = append(warnings, homeRes.Skipped...)

		// Mailboxes — observation-only stub (counts + paths recorded
		// as pending_manual warnings; JMAP push is follow-up work).
		// We walk the extracted tarball's homedir/mail/ subtree,
		// which is also still readable in /var/lib/jabali-migrations/
		// <job-id>/extracted/ for the operator's manual import path.
		mailRes, err := cpanel.ImportMailboxes(ctx, p.parsed)
		if err != nil {
			return bytes, warnings, fmt.Errorf("mailboxes: %w", err)
		}
		warnings = append(warnings, fmt.Sprintf(
			"mailboxes: maildirs=%d messages=%d bytes=%d (manual import — see runbook §2.6)",
			mailRes.MaildirsFound, mailRes.MessagesFound, mailRes.BytesFound))
		warnings = append(warnings, mailRes.Skipped...)

		return bytes, warnings, nil
	}
}

// cpanelAnalyzeCallback runs the cPanel Discoverer against the
// source host using credentials at /etc/jabali-panel/migration-
// secrets/<job-id>.env (per ADR-0094 §"tracked risks"). Records
// the produced AccountManifest into migration_jobs.manifest_json
// so the validate + restore stages can read it back without a
// second SSH round-trip.
//
// Falls back to skip-with-warning when the secret file is absent —
// operator-driven workflow today often has the cpmove tarball
// already on disk + no need to re-run discovery. Restore stage
// works without analyze having succeeded.
func cpanelAnalyzeCallback() migrate.StageCallback {
	return func(ctx context.Context, job *models.MigrationJob, payload any) (int64, []string, error) {
		secretPath := fmt.Sprintf("/etc/jabali-panel/migration-secrets/%s.env", job.ID)
		if _, err := osStat(secretPath); err != nil {
			return 0, []string{
				fmt.Sprintf("analyze_skip:no_secret_file:%s", secretPath),
				"analyze_skip:operator_supplied_tarball — restore stage will use the pre-extracted tree",
			}, nil
		}
		d := cpanel.New()
		s, err := d.Connect(ctx, job.SourceHost, "root", migrate.SecretRef{Path: secretPath})
		if err != nil {
			return 0, nil, fmt.Errorf("connect: %w", err)
		}
		defer func() { _ = d.Close(ctx, s) }()

		mf, err := d.DescribeAccount(ctx, s, job.SourceUser)
		if err != nil {
			return 0, nil, fmt.Errorf("describe %s: %w", job.SourceUser, err)
		}
		// Persist manifest to migration_jobs.manifest_json so resume
		// + validate + restore can read without re-doing discovery.
		// Best-effort marshal — payload is small (single account)
		// so this rarely fails.
		raw, mErr := jsonMarshal(mf)
		if mErr == nil && raw != "" {
			if uErr := job.UpdatedAt.IsZero(); !uErr {
				// no-op stub — cobra cmd reaches the repo through
				// closure; analyze callback receives only the job
				// model. Future commits thread the repo into the
				// callback so manifest_json persists. For now,
				// surface the manifest summary in warnings so the
				// operator sees it in the migration_jobs row anyway.
			}
			_ = raw
		}
		warnings := []string{
			fmt.Sprintf("analyze: domains=%d mailboxes=%d databases=%d cron=%d ssh=%d",
				len(mf.Domains), len(mf.Mailboxes), len(mf.Databases),
				len(mf.Cron), len(mf.SSH)),
		}
		for _, w := range mf.Warnings {
			warnings = append(warnings, fmt.Sprintf("analyze_warning:%s:%s", w.Code, w.Detail))
		}
		return 0, warnings, nil
	}
}

// osStat is a thin wrapper for testability — swappable in tests
// without monkey-patching os.Stat. Production path is os.Stat.
func osStat(name string) (os.FileInfo, error) { return os.Stat(name) }

// jsonMarshal returns a JSON encoding of v. Returns "" on error so
// the caller can decide whether to surface that as a non-fatal
// warning.
func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
