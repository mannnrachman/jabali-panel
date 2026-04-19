package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// newKratosMigrateCmd builds the `jabali kratos-migrate` Cobra subcommand
// described in the M20 blueprint §1 step 4. It backfills Kratos identities
// for pre-M20 users whose `kratos_identity_id` column is still NULL.
//
// Operator flow:
//  1. Run `--dry-run` first — walks the table, logs per-user decisions
//     (skip vs migrate) without calling Kratos write APIs. The canary STILL
//     runs in dry-run because it's a read-only round trip that validates
//     bcrypt passthrough before any real batch.
//  2. Read the dry-run summary. If it looks right, re-run without the flag.
//  3. On partial failure, failed rows are logged + kept unmigrated; re-run
//     is safe (idempotent) and will retry only the still-NULL rows.
func newKratosMigrateCmd() *cobra.Command {
	var (
		dryRun      bool
		batchSize   int
		skipCanary  bool
		totpOnly    bool
	)

	cmd := &cobra.Command{
		Use:   "kratos-migrate",
		Short: "Backfill Kratos identities for existing panel users (M20)",
		Long: `Import every panel user whose kratos_identity_id is NULL into Ory Kratos
using bcrypt passthrough (users keep their existing passwords without
re-enrolment).

Safety:
  - A bcrypt passthrough canary runs before any real write. If Kratos
    rejects our hash format, the batch aborts without touching users.
  - Idempotent: already-migrated users (non-NULL kratos_identity_id,
    or email that already exists in Kratos) are skipped.
  - Per-row failures are logged and counted; the tool exits non-zero
    if any row failed.

Flags:
  --dry-run          Plan only. Canary runs, but no identities are written.
  --batch-size       Users processed per log-progress batch (default 50).
  --skip-canary      Dangerous: skip the bcrypt passthrough check.
  --totp-only        Reserved for step 8 (TOTP migration). Currently returns
                     "not yet implemented" so operators don't think their
                     password-only run succeeded when TOTP is still expected.`,
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runKratosMigrate(cmd.Context(), kratosMigrateOptions{
				DryRun:     dryRun,
				BatchSize:  batchSize,
				SkipCanary: skipCanary,
				TOTPOnly:   totpOnly,
			})
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview migration decisions without writing to Kratos")
	cmd.Flags().IntVar(&batchSize, "batch-size", 50, "users per logged-progress batch")
	cmd.Flags().BoolVar(&skipCanary, "skip-canary", false, "skip bcrypt passthrough check (dangerous)")
	cmd.Flags().BoolVar(&totpOnly, "totp-only", false, "migrate only TOTP credentials (reserved for M20 step 8)")
	return cmd
}

type kratosMigrateOptions struct {
	DryRun     bool
	BatchSize  int
	SkipCanary bool
	TOTPOnly   bool
}

type kratosMigrateSummary struct {
	Total     int
	Migrated  int
	Skipped   int
	Failed    int
	Unchanged int // dry-run rows that would have been migrated
}

func runKratosMigrate(ctx context.Context, opts kratosMigrateOptions) error {
	log := sharedLog
	if log == nil {
		log = slog.Default()
	}

	if opts.TOTPOnly {
		return fmt.Errorf("--totp-only is reserved for M20 step 8 and not yet implemented; do not rely on this run to migrate TOTP")
	}

	if sharedCfg.Auth.Provider != "kratos" {
		log.Warn("auth.provider is not \"kratos\" — migration will run but identities won't be used until cutover",
			"current_provider", sharedCfg.Auth.Provider)
	}

	if sharedCfg.Auth.Kratos.PublicURL == "" || sharedCfg.Auth.Kratos.AdminURL == "" {
		return fmt.Errorf("auth.kratos.public_url or auth.kratos.admin_url is empty — cannot reach Kratos")
	}
	client := kratosclient.NewClient(sharedCfg.Auth.Kratos.PublicURL, sharedCfg.Auth.Kratos.AdminURL)

	log.Info("kratos-migrate starting",
		"dry_run", opts.DryRun,
		"batch_size", opts.BatchSize,
		"skip_canary", opts.SkipCanary,
		"public_url", sharedCfg.Auth.Kratos.PublicURL,
		"admin_url", sharedCfg.Auth.Kratos.AdminURL)

	if !opts.SkipCanary {
		log.Info("running bcrypt passthrough canary")
		if err := client.VerifyBcryptPassthrough(ctx); err != nil {
			return fmt.Errorf("canary failed: %w", err)
		}
		log.Info("canary passed — bcrypt passthrough is working")
	} else {
		log.Warn("--skip-canary set — bcrypt passthrough will NOT be verified before the batch")
	}

	// Pre-fetch the existing Kratos email→id map so we can skip rows whose
	// identity already exists upstream even if the panel row forgot its
	// kratos_identity_id (e.g. partial prior run). One admin-API scan, O(N).
	existing, err := client.AllIdentitiesByEmail(ctx)
	if err != nil {
		return fmt.Errorf("scan existing kratos identities: %w", err)
	}
	log.Info("scanned existing kratos identities", "count", len(existing))

	var users []models.User
	if err := sharedDB.WithContext(ctx).
		Where("kratos_identity_id IS NULL").
		Order("created_at ASC").
		Find(&users).Error; err != nil {
		return fmt.Errorf("fetch unmigrated users: %w", err)
	}

	summary := kratosMigrateSummary{Total: len(users)}
	log.Info("fetched unmigrated users", "count", summary.Total)

	for i, u := range users {
		if (i+1)%opts.BatchSize == 0 || i+1 == summary.Total {
			log.Info("progress", "processed", i+1, "total", summary.Total)
		}

		traits := kratosclient.AdminTraits{
			Email:   u.Email,
			IsAdmin: u.IsAdmin,
		}
		if u.Username != nil {
			traits.Username = *u.Username
		}

		// Idempotency check: identity already exists in Kratos with this email.
		if existingID, ok := existing[strings.ToLower(u.Email)]; ok {
			if opts.DryRun {
				log.Info("would link existing kratos identity", "user_id", u.ID, "email", u.Email, "identity_id", existingID)
			} else {
				if err := sharedDB.WithContext(ctx).
					Model(&models.User{}).
					Where("id = ? AND kratos_identity_id IS NULL", u.ID).
					Update("kratos_identity_id", existingID).Error; err != nil {
					log.Error("link existing identity: db update failed", "user_id", u.ID, "error", err)
					summary.Failed++
					continue
				}
			}
			summary.Skipped++
			continue
		}

		if opts.DryRun {
			log.Info("would create kratos identity",
				"user_id", u.ID,
				"email", u.Email,
				"is_admin", u.IsAdmin,
				"hash_len", len(u.PasswordHash))
			summary.Unchanged++
			continue
		}

		identityID, err := client.CreateIdentityWithPassword(ctx, traits, u.PasswordHash)
		if err != nil {
			log.Error("create kratos identity failed", "user_id", u.ID, "email", u.Email, "error", err)
			summary.Failed++
			continue
		}

		// Race-safe update: the WHERE guards against an overlapping admin-POST
		// that already set kratos_identity_id between fetch and update. If
		// another path wrote first, we orphan our just-created identity and
		// clean it up rather than shadow-overwriting the other ID.
		res := sharedDB.WithContext(ctx).
			Model(&models.User{}).
			Where("id = ? AND kratos_identity_id IS NULL", u.ID).
			Update("kratos_identity_id", identityID)
		if res.Error != nil {
			log.Error("db update failed — orphaning kratos identity, rolling back", "user_id", u.ID, "identity_id", identityID, "error", res.Error)
			if delErr := client.DeleteIdentity(ctx, identityID); delErr != nil {
				log.Error("rollback delete failed — orphan left in kratos", "identity_id", identityID, "error", delErr)
			}
			summary.Failed++
			continue
		}
		if res.RowsAffected == 0 {
			log.Warn("user row changed or vanished between fetch and update — deleting orphan identity", "user_id", u.ID, "identity_id", identityID)
			if delErr := client.DeleteIdentity(ctx, identityID); delErr != nil {
				log.Error("rollback delete failed — orphan left in kratos", "identity_id", identityID, "error", delErr)
			}
			summary.Skipped++
			continue
		}

		summary.Migrated++
	}

	log.Info("kratos-migrate complete",
		"total", summary.Total,
		"migrated", summary.Migrated,
		"skipped_already_in_kratos", summary.Skipped,
		"would_migrate_dry_run", summary.Unchanged,
		"failed", summary.Failed)

	if summary.Failed > 0 {
		return fmt.Errorf("migration finished with %d failures (see log above)", summary.Failed)
	}
	return nil
}
