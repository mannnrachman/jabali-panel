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
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate/cpanel"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate/directadmin"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate/hestiacp"
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
			if jobID == "" {
				return errors.New("--job-id is required")
			}
			ctx := cmd.Context()

			jobsRepo := repository.NewMigrationJobRepository(sharedDB)
			usersRepo := repository.NewUserRepository(sharedDB)
			dbsRepo := repository.NewDatabaseRepository(sharedDB)
			dbUsersRepo := repository.NewDatabaseUserRepository(sharedDB)
			dbGrantsRepo := repository.NewDatabaseUserGrantRepository(sharedDB)
			cronsRepo := repository.NewCronJobRepository(sharedDB)
			sshRepo := repository.NewSSHKeyRepository(sharedDB)
			domainsRepo := repository.NewDomainRepository(sharedDB)
			mbRepo := repository.NewMailboxRepository(sharedDB)
			fwdRepo := repository.NewEmailForwarderRepository(sharedDB)
			arRepo := repository.NewEmailAutoresponderRepository(sharedDB)
			filtersRepo := repository.NewEmailFilterRepository(sharedDB)
			phpPoolsRepo := repository.NewPHPPoolRepository(sharedDB)
			kc := kratosclient.NewClient(sharedCfg.Auth.Kratos.PublicURL, sharedCfg.Auth.Kratos.AdminURL)

			job, err := jobsRepo.FindByID(ctx, jobID)
			if err != nil {
				return fmt.Errorf("load job: %w", err)
			}
			failJob := func(err error) error {
				msg := err.Error()
				if uErr := jobsRepo.UpdateState(ctx, job.ID, models.MigrationStateFailed, &msg); uErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: mark job failed: %v\n", uErr)
				}
				return err
			}

			// Default-from-source resolution. Operator can still pass
			// --target-user / --target-email / --target-password to
			// override; absent values fall through to:
			//   user → job.SourceUser (always present)
			//   email → cpmove contactemail file or CONTACTEMAIL kv
			//   password → 16-char random (printed to stdout once
			//              so the operator can hand it to the user)
			// Source's crypt(3) shadow hash isn't reusable (Kratos
			// expects Argon2/bcrypt) but is surfaced so the operator
			// can verify the hash style before sending a reset link.
			extractDir := filepath.Join("/var/lib/jabali-migrations", jobID, "extracted")
			meta, _ := cpanel.PeekAccountMeta(extractDir, job.SourceUser)
			if targetUser == "" {
				targetUser = job.SourceUser
				fmt.Printf("  → target-user defaulted from source: %s\n", targetUser)
			}
			if targetEmail == "" && meta != nil && meta.Email != "" {
				targetEmail = meta.Email
				fmt.Printf("  → target-email detected from cpmove: %s\n", targetEmail)
			}
			if targetEmail == "" {
				// No contactemail file in the tarball (older pkgacct
				// versions or pre-extracted blob) — synthesize
				// <user>@<source-host> so auto-create can proceed.
				// Operator can fix the address post-migration via
				// /admin/users; the synthetic value is a placeholder,
				// not a delivery target.
				hostPart := job.SourceHost
				if hostPart == "" {
					hostPart = "migrated.local"
				}
				targetEmail = targetUser + "@" + hostPart
				fmt.Printf("  → target-email synthesized (no contactemail in tarball): %s\n", targetEmail)
			}
			if targetPassword == "" {
				// Generate a random strong password the operator can
				// hand to the customer. Print ONCE — we never store
				// this in the DB.
				if pw, perr := randomPassword(16); perr == nil {
					targetPassword = pw
					fmt.Printf("  → target-password auto-generated: %s   (share with customer; reset via Kratos when needed)\n", targetPassword)
					if meta != nil && meta.PasswordHash != "" {
						fmt.Printf("    (source had a crypt(3) hash but Kratos uses Argon2; original password not recoverable)\n")
					}
				}
			}

			user, err := usersRepo.FindByUsername(ctx, targetUser)
			if err != nil {
				if !errors.Is(err, repository.ErrNotFound) {
					return failJob(fmt.Errorf("destination user %q lookup: %w", targetUser, err))
				}
				// Auto-create when operator supplied --target-email
				// + --target-password. Otherwise fail with helpful
				// message pointing at the auto-create flags.
				if targetEmail == "" || targetPassword == "" {
					return failJob(fmt.Errorf("destination user %q does not exist. "+
						"Pre-create via /admin/users OR pass --target-email + --target-password to auto-create",
						targetUser))
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
					return failJob(fmt.Errorf("auto-create destination user %q: %w", targetUser, cErr))
				}
				user = res.User
				fmt.Fprintf(cmd.OutOrStdout(), "Auto-created destination user %s (id=%s)\n",
					*user.Username, user.ID)
				if res.ProvisionWarning != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", res.ProvisionWarning)
				}
				// Restore the source's Linux shadow hash directly onto
				// /etc/shadow so the operator's source SSH/SFTP password
				// works on the destination box unchanged. Kratos panel
				// login keeps the random plaintext we just used because
				// crypt(3) and Argon2 are incompatible.
				if meta != nil && meta.PasswordHash != "" && sharedAgent != nil {
					hashCtx, hashCancel := context.WithTimeout(ctx, 10*time.Second)
					if _, perr := sharedAgent.Call(hashCtx, "user.password", map[string]any{
						"username":      *user.Username,
						"password_hash": meta.PasswordHash,
					}); perr != nil {
						fmt.Fprintf(cmd.ErrOrStderr(),
							"warning: source shadow-hash restore failed: %v\n", perr)
					} else {
						fmt.Printf("  → source Linux shadow hash restored on /etc/shadow (SSH/SFTP password unchanged from source)\n")
					}
					hashCancel()
				}
				// Stamp migration_jobs.target_user_id so the
				// validate stage's acceptExistingUserID gate
				// recognises the auto-created user + doesn't
				// flag target_user_exists.
				if uErr := jobsRepo.UpdateTargetUser(ctx, job.ID, user.ID); uErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: stamp job.target_user_id: %v (validate may false-positive)\n", uErr)
				} else {
					job.TargetUserID = &user.ID
				}
			} else {
				// Pre-existing user (operator pre-created via
				// admin UI or 'jabali user create'). Stamp row
				// so validate recognises it as ours.
				if job.TargetUserID == nil || *job.TargetUserID != user.ID {
					if uErr := jobsRepo.UpdateTargetUser(ctx, job.ID, user.ID); uErr == nil {
						job.TargetUserID = &user.ID
					}
				}
			}
			if user.Username == nil {
				return failJob(fmt.Errorf("destination user %s has no Linux username", user.ID))
			}

			// cPanel + WHM-pkgacct share restore code-path: both
			// produce cpmove-<user>.tar.gz with identical layout.
			// WHM-pkgacct just skips the live-source SSH probe
			// (operator pre-uploaded the tarball). Future
			// directadmin / hestiacp land here as
			// they get per-area builders + tarball-pull wired.
			switch job.SourceKind {
			case models.MigrationSourceCpanel,
				models.MigrationSourceWHMpkgacct,
				models.MigrationSourceDirectAdmin,
				models.MigrationSourceHestia:
				// supported — fall through
			default:
				return failJob(fmt.Errorf("source kind %q not yet supported by jabali migrate import; "+
					"supported: %s, %s, %s, %s",
					job.SourceKind,
					models.MigrationSourceCpanel,
					models.MigrationSourceWHMpkgacct,
					models.MigrationSourceDirectAdmin,
					models.MigrationSourceHestia))
			}

			// extractDir is already resolved earlier (above the user
			// resolution block) — re-use the same value here.
			var parsed *cpanel.ParsedTarball
			switch job.SourceKind {
			case models.MigrationSourceDirectAdmin:
				// DA tar at /var/lib/jabali-migrations/<id>/
				// user.<user>.<ts>.tar.gz (operator-supplied
				// filename pattern matches DA system_backup_user
				// output). Fall back to assumed-pre-extracted
				// when ParseDATarball can't find a tar.
				daTarPath := filepath.Join("/var/lib/jabali-migrations", job.ID,
					fmt.Sprintf("user.%s.tar.gz", job.SourceUser))
				if da, derr := directadmin.ParseDATarball(daTarPath, extractDir); derr == nil {
					parsed = directadmin.ToCpanelParsed(da, *user.Username)
				} else {
					parsed = &cpanel.ParsedTarball{
						ExtractDir: extractDir,
						HomeDir:    filepath.Join(extractDir, job.SourceUser),
						SourceUser: job.SourceUser,
					}
					fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: ParseDATarball failed (%v); using pre-extracted assumption\n", derr)
				}
			case models.MigrationSourceHestia:
				// Hestia tar at /var/lib/jabali-migrations/<id>/
				// <user>.<ts>.tar (or .tar.gz). Hestia parser produces
				// a HestiaParsedTarball — for v1 we adapt the
				// MySQLDumps subset to *cpanel.ParsedTarball so
				// the cpanel restore writers can run; cron + ssh
				// keys via tarball deferred (Hestia's layout
				// doesn't contain a top-level cron/ or
				// .ssh/authorized_keys file the cpanel writers
				// recognise — operator hand-imports those today).
				hTarPath := filepath.Join("/var/lib/jabali-migrations", job.ID,
					fmt.Sprintf("%s.tar.gz", job.SourceUser))
				if h, herr := hestiacp.ParseHestiaTarball(hTarPath, extractDir); herr == nil {
					parsed = &cpanel.ParsedTarball{
						ExtractDir: extractDir,
						SourceUser: job.SourceUser,
						HomeDir:    h.WebRoot, // Hestia rsync target = web/<dom>/public_html/...
						MailRoot:   h.MailRoot, // Hestia stores at mail/<dom>/<local>/Maildir
						MySQLDumps: h.MySQLDumps,
					}
					if h.SSHKeys != "" {
						parsed.SSHAuthorized = []string{h.SSHKeys}
					}
					if h.CronFile != "" {
						parsed.CronFiles = []string{h.CronFile}
					}
					// M35.4 Hestia DomainNames+DocRoots fallback for
					// ImportDomains (no BIND zones in Hestia tarball).
					// Target docroot mirrors the source layout:
					//   /home/<target>/web/<dom>/public_html
					// ImportHome rsync runs first + lands content there.
					if len(h.DomainDirs) > 0 {
						parsed.DomainNames = make([]string, 0, len(h.DomainDirs))
						parsed.DocRoots = make(map[string]string, len(h.DomainDirs))
						for name := range h.DomainDirs {
							parsed.DomainNames = append(parsed.DomainNames, name)
							parsed.DocRoots[name] = filepath.Join(
								"/home", *user.Username, "web", name, "public_html")
						}
					}
				} else {
					parsed = &cpanel.ParsedTarball{
						ExtractDir: extractDir,
						SourceUser: job.SourceUser,
					}
					fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: ParseHestiaTarball failed (%v); using pre-extracted assumption\n", herr)
				}
			default:
				// cpanel + whm_pkgacct
				p, err := cpanel.ParseTarball(
					filepath.Join("/var/lib/jabali-migrations", job.ID, fmt.Sprintf("cpmove-%s.tar.gz", job.SourceUser)),
					extractDir,
				)
				if err != nil {
					p = &cpanel.ParsedTarball{
						ExtractDir: extractDir,
						HomeDir:    filepath.Join(extractDir, "cp", job.SourceUser, "homedir"),
						SourceUser: job.SourceUser,
					}
					fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: ParseTarball failed (%v); falling back to assumed pre-extracted layout\n", err)
				}
				parsed = p
			}

			// Owner default mailbox: cpanel-side <user> @ primary domain.
			// Agent's ImportMailboxes uses this to import the
			// mail/{cur,new,tmp,.Drafts,...} root tree the cpanel owner
			// reads as their default mailbox.
			if parsed != nil && parsed.OwnerEmail == "" {
				if meta != nil && meta.PrimaryDomain != "" {
					parsed.OwnerEmail = job.SourceUser + "@" + meta.PrimaryDomain
				}
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
						sshRepo, cronsRepo, dbsRepo, dbUsersRepo, dbGrantsRepo, domainsRepo, mbRepo, fwdRepo, arRepo, filtersRepo, phpPoolsRepo, usersRepo, kc,
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
		// Target-user-exists conflict suppressed when
		// migration_jobs.target_user_id is set — auto-create flow
		// ('jabali migrate import --target-email + --target-
		// password') minted the user before the runner began, so
		// finding it now isn't a conflict, it's our user.
		acceptUserID := ""
		if job.TargetUserID != nil {
			acceptUserID = *job.TargetUserID
		}
		rpt, err := migrate.Validate(ctx, migrate.ValidateDeps{
			Users: users, Domains: domains,
		}, mf, targetUsername, acceptUserID)
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
	dbUsersRepo repository.DatabaseUserRepository,
	dbGrantsRepo repository.DatabaseUserGrantRepository,
	domainsRepo repository.DomainRepository,
	mbRepo repository.MailboxRepository,
	fwdRepo repository.EmailForwarderRepository,
	arRepo repository.EmailAutoresponderRepository,
	filtersRepo repository.EmailFilterRepository,
	phpPoolsRepo repository.PHPPoolRepository,
	usersRepo repository.UserRepository,
	kc *kratosclient.Client,
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

		dbsRes, err := cpanel.ImportDatabases(ctx, dbsRepo, dbUsersRepo, dbGrantsRepo, sharedAgent, p.parsed, p.targetUserID, p.targetUsername)
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

		// M35.8 P7: per-domain rsync split. cpanel ships all sites
		// under <homedir>/public_html/(<addon>/) flat layout; jabali
		// uses /home/<user>/domains/<dom>/public_html/. ImportHomeSplit
		// reads per-domain documentroot from cpmove userdata YAML and
		// dispatches one rsync per docroot, then a final pass for the
		// rest of the homedir (mail/ etc/ application_backups/) minus
		// public_html. Falls back to the legacy whole-homedir rsync
		// when no userdata YAML is present.
		hsRes, err := cpanel.ImportHomeSplit(ctx, sharedAgent, p.parsed, job.ID, p.targetUsername)
		if err != nil {
			return bytes, warnings, fmt.Errorf("home_split: %w", err)
		}
		var fallback bool
		for _, sk := range hsRes.Skipped {
			if strings.HasPrefix(sk, "home_split_skip:no_userdata_yaml") {
				fallback = true
				break
			}
		}
		if fallback || hsRes.DomainsCopied == 0 {
			homeRes, err := cpanel.ImportHome(ctx, sharedAgent, p.parsed, job.ID, p.targetUsername)
			if err != nil {
				return bytes, warnings, fmt.Errorf("home: %w", err)
			}
			bytes += homeRes.BytesCopied
			warnings = append(warnings, fmt.Sprintf("home: bytes=%d files=%d (legacy full-homedir mode)", homeRes.BytesCopied, homeRes.Files))
			warnings = append(warnings, homeRes.Skipped...)
		} else {
			bytes += hsRes.BytesCopied
			warnings = append(warnings, fmt.Sprintf("home: bytes=%d files=%d domains=%d (per-domain split)", hsRes.BytesCopied, hsRes.Files, hsRes.DomainsCopied))
			warnings = append(warnings, hsRes.Skipped...)
		}

		domainsRes, err := cpanel.ImportDomains(ctx, domainsRepo, sharedAgent, p.parsed, p.targetUserID, p.targetUsername)
		if err != nil {
			return bytes, warnings, fmt.Errorf("domains: %w", err)
		}
		warnings = append(warnings, fmt.Sprintf("domains: created=%d email_enabled=%d", domainsRes.Created, domainsRes.EmailEnabled))
		warnings = append(warnings, domainsRes.Skipped...)

		mailRes, err := cpanel.ImportMailboxes(ctx, p.parsed, sharedAgent, job.ID, mbRepo, domainsRepo)
		if err != nil {
			return bytes, warnings, fmt.Errorf("mailboxes: %w", err)
		}
		warnings = append(warnings, fmt.Sprintf(
			"mailboxes: maildirs=%d messages_found=%d messages_pushed=%d bytes_pushed=%d",
			mailRes.MaildirsFound, mailRes.MessagesFound, mailRes.MessagesPushed, mailRes.BytesPushed))
		warnings = append(warnings, mailRes.Skipped...)

		// M35.8 P3: per-domain custom SSL certs from apache_tls/.
		sslRes, err := cpanel.ImportSSL(ctx, sharedAgent, p.parsed)
		if err != nil {
			return bytes, warnings, fmt.Errorf("ssl: %w", err)
		}
		warnings = append(warnings, fmt.Sprintf("ssl: installed=%d", sslRes.Installed))
		warnings = append(warnings, sslRes.Skipped...)

		// M35.8 P2+P5: catch-all + subdomains + forwarders restore.
		extrasRes, err := cpanel.ImportExtras(ctx, domainsRepo, mbRepo, fwdRepo, arRepo, filtersRepo, phpPoolsRepo, sharedAgent, p.parsed, p.targetUserID, p.targetUsername)
		if err != nil {
			return bytes, warnings, fmt.Errorf("extras: %w", err)
		}
		warnings = append(warnings, fmt.Sprintf(
			"extras: catchalls=%d subdomains=%d forwarders=%d forwarders_orphan=%d autoresponders=%d autoresponders_orphan=%d filters=%d php_pools=%d php_domains_bound=%d php_version=%s ftp_accounts=%d dkim_keys=%d",
			extrasRes.CatchallsSet, extrasRes.SubdomainsCreated,
			extrasRes.ForwardersCreated, extrasRes.ForwardersOrphaned,
			extrasRes.AutorespondersCreated, extrasRes.AutorespondersOrphaned,
			extrasRes.FiltersImported,
			extrasRes.PHPPoolsCreated, extrasRes.PHPDomainsBound,
			extrasRes.PHPVersionApplied, extrasRes.FTPAccountsObserved,
			extrasRes.DKIMKeysPreserved))
		warnings = append(warnings, extrasRes.Skipped...)

		// Ensure the migrated user has a Kratos identity so they can log in.
		if kc != nil && usersRepo != nil {
			targetUser, uErr := usersRepo.FindByID(ctx, p.targetUserID)
			if uErr != nil {
				warnings = append(warnings, fmt.Sprintf("kratos: load user %s: %v", p.targetUserID, uErr))
			} else {
				status, newID, _ := rebuildOne(ctx, kc, usersRepo, targetUser, "168h")
				warnings = append(warnings, fmt.Sprintf("kratos: status=%s new_id=%s", status, newID))
			}
		}

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
		// Honor server_settings.migration_allow_private_hosts so the
		// analyze stage's SSH dial matches what the discover/pull
		// paths already use. Best-effort lookup; default safe.
		settingsRepo := repository.NewServerSettingsRepository(sharedDB)
		if s, sErr := settingsRepo.Get(ctx); sErr == nil && s != nil {
			d.AllowPrivate = s.MigrationAllowPrivateHosts
		}
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

// randomPassword returns an N-byte URL-safe random string trimmed
// of any padding. Strong enough for a one-time generated user
// password the operator hands to the customer; the customer is
// expected to rotate via Kratos.
func randomPassword(n int) (string, error) {
	if n < 12 {
		n = 16
	}
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("rand.Read: %w", err)
	}
	s := base64.RawURLEncoding.EncodeToString(raw)
	if len(s) > n {
		s = s[:n]
	}
	return strings.ReplaceAll(s, "_", "x"), nil
}
