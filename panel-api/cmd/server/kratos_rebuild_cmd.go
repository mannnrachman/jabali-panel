package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// newAdminRebuildKratosCmd implements the DB-loss recovery path documented in
// ADR-0034: if the Kratos database is lost and no backup exists, the panel's
// users.kratos_identity_id FKs are dangling. Stand up a fresh Kratos, then
// run this command to mint a new identity for every user, relink them, and
// emit a CSV of recovery-link URLs the operator can distribute out-of-band.
//
// NOT a routine operation — this rewrites every kratos_identity_id in the
// users table. Always --dry-run first.
func newAdminRebuildKratosCmd() *cobra.Command {
	var (
		outputPath string
		dryRun     bool
		assumeYes  bool
		expiresIn  string
	)
	cmd := &cobra.Command{
		Use:   "rebuild-kratos",
		Short: "Recreate Kratos identities from panel users (DB-loss recovery, ADR-0034)",
		Long: `Rebuilds every Kratos identity from the panel users table. For each row
including those with NULL kratos_identity_id (disaster recovery):

  1. If kratos_identity_id exists, probes Kratos to check if it's still valid.
     If valid, skips the user. If invalid or NULL, creates a new identity.
  2. Mint a NEW Kratos identity with the user's traits (email, username,
     is_admin) and a random cost-12 bcrypt temporary password that is
     never exposed to the operator or user.
  3. Relink the panel row's kratos_identity_id to the new UUID via the
     same compensating-transaction plumbing used by M20 user-create.
     If the relink fails, the newly-created Kratos identity is deleted
     so the rebuild stays atomic per user.
  4. Generate a one-click recovery link for that identity and append a
     row to the CSV output (email, new_kratos_id, recovery_link).

Always run with --dry-run first. --yes skips the interactive prompt.`,
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			// Build the admin-facing Kratos client from the same config the
			// live service uses. Requires Auth.Kratos.AdminURL to be set.
			kratosCfg := sharedCfg.Auth.Kratos
			if kratosCfg.AdminURL == "" {
				return fmt.Errorf("auth.kratos.admin_url not configured — cannot rebuild without admin API access")
			}
			kc := kratosclient.NewClient(kratosCfg.PublicURL, kratosCfg.AdminURL)

			// Target set: every panel user (including NULL kratos_identity_id for disaster recovery).
			// Note we don't filter by "still valid in Kratos" — the whole
			// point of this command is that those UUIDs are now dangling.
			var targets []models.User
			if err := sharedDB.WithContext(ctx).
				Order("is_admin DESC, email ASC").
				Find(&targets).Error; err != nil {
				return fmt.Errorf("list users: %w", err)
			}
			if len(targets) == 0 {
				fmt.Println("No users found — nothing to rebuild.")
				return nil
			}

			fmt.Printf("Plan: rebuild %d Kratos %s from panel users table.\n",
				len(targets), pluralize(len(targets), "identity", "identities"))
			fmt.Printf("  Kratos admin URL: %s\n", kratosCfg.AdminURL)
			fmt.Printf("  Output CSV:       %s\n", outputPath)

			if dryRun {
				fmt.Println("\n(dry-run) would rebuild:")
				for _, u := range targets {
					fmt.Printf("  - %s  (old kratos_id=%s, admin=%t)\n",
						u.Email, derefOrEmpty(u.KratosIdentityID), u.IsAdmin)
				}
				return nil
			}

			if !assumeYes {
				fmt.Printf("\nThis rewrites users.kratos_identity_id for %d rows. Continue? [y/N] ", len(targets))
				reader := bufio.NewReader(os.Stdin)
				line, _ := reader.ReadString('\n')
				if !strings.EqualFold(strings.TrimSpace(line), "y") {
					return fmt.Errorf("aborted by operator")
				}
			}

			// CSV open before we start — no point rebuilding if we can't
			// emit the tokens. Write header first.
			f, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				return fmt.Errorf("open output csv: %w", err)
			}
			defer f.Close()
			w := csv.NewWriter(f)
			defer w.Flush()
			if err := w.Write([]string{"email", "kratos_identity_id", "recovery_link", "status"}); err != nil {
				return fmt.Errorf("write csv header: %w", err)
			}

			users := userRepo()
			var rebuilt, failed, linkMissing, skipped int
			for _, u := range targets {
				status, newKratosID, link := rebuildOne(ctx, kc, users, &u, expiresIn)
				switch status {
				case statusOK:
					rebuilt++
				case statusRecoveryMissing:
					rebuilt++
					linkMissing++
				case statusSkippedLive:
					skipped++
				default:
					failed++
				}
				row := []string{u.Email, newKratosID, link, string(status)}
				if err := w.Write(row); err != nil {
					return fmt.Errorf("write csv row: %w", err)
				}
				// Flush after each row so a crash partway through still
				// leaves the operator with partial progress they can use.
				w.Flush()
				fmt.Printf("  [%s] %s → %s\n", status, u.Email, shortID(newKratosID))
			}

			fmt.Printf("\nSummary: %d rebuilt (%d without recovery link), %d skipped (already live), %d failed.\n",
				rebuilt, linkMissing, skipped, failed)
			fmt.Printf("CSV: %s\n", outputPath)
			if linkMissing > 0 {
				fmt.Println("\n! Rows with an empty recovery_link were relinked but the admin/recovery/code call failed;")
				fmt.Println("  re-run `hydra`/`kratos identities get <id>` or manually POST /admin/recovery/code for those.")
			}
			if failed > 0 {
				return fmt.Errorf("%d user(s) failed — see CSV status column", failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outputPath, "output", "/tmp/jabali-recovery-tokens.csv",
		"CSV file to emit (email, new kratos_identity_id, recovery_link, status)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "List target users and exit without writing")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip interactive confirmation prompt")
	cmd.Flags().StringVar(&expiresIn, "expires-in", "24h",
		"Kratos recovery-code TTL (e.g. 1h, 24h) — operators typically need ≥24h to distribute")
	return cmd
}

// rebuildStatus summarizes the per-user outcome for the CSV status column
// and the summary counts. Kept as a typed string so the CSV reads cleanly.
type rebuildStatus string

const (
	statusOK              rebuildStatus = "ok"
	statusRecoveryMissing rebuildStatus = "ok_no_link"
	statusSkippedLive     rebuildStatus = "skipped_live"
	statusCreateFailed    rebuildStatus = "create_failed"
	statusLinkFailed      rebuildStatus = "link_failed"
	statusProbeFailed     rebuildStatus = "probe_failed"
)

// rebuildOne does the per-user work. Returns status + the new Kratos UUID
// (empty on pre-create failure) + the recovery link (empty when the code
// endpoint failed AFTER a successful relink — operator can regenerate).
// Takes `users` as a parameter (rather than calling userRepo() internally)
// so unit tests can inject a mock without touching sharedDB.
//
// Idempotency: before minting a new identity, probe Kratos for the
// current kratos_identity_id. If it resolves (200 OK), the user is
// already linked to a live identity and we skip them — useful when
// recovering from a partial rebuild (e.g. operator Ctrl-C'd a prior
// run halfway through) or when only SOME of the Kratos DB was lost.
// Only ErrIdentityNotFound triggers the actual rebuild; any other
// error (network/5xx) returns statusProbeFailed so the operator sees
// something is wrong with Kratos itself before we start mutating.
func rebuildOne(ctx context.Context, kc *kratosclient.Client, users repository.UserRepository, u *models.User, expiresIn string) (rebuildStatus, string, string) {
	currentKratosID := derefOrEmpty(u.KratosIdentityID)
	if currentKratosID != "" {
		_, err := kc.GetIdentity(ctx, currentKratosID)
		switch {
		case err == nil:
			// Identity still live — don't touch it. Leave the panel row
			// linked to the existing (working) identity.
			return statusSkippedLive, currentKratosID, ""
		case errors.Is(err, kratosclient.ErrIdentityNotFound):
			// Expected in the DB-loss case — fall through to rebuild.
		default:
			fmt.Fprintf(os.Stderr, "  ! %s: GetIdentity probe failed: %v (skipping to avoid mutating on a broken Kratos)\n", u.Email, err)
			return statusProbeFailed, currentKratosID, ""
		}
	}

	tempHash, err := genTempBcrypt()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ! %s: temp-password hash failed: %v\n", u.Email, err)
		return statusCreateFailed, "", ""
	}

	traits := kratosclient.AdminTraits{
		Email:   u.Email,
		IsAdmin: u.IsAdmin,
	}
	if u.Username != nil {
		traits.Username = *u.Username
	}

	newID, err := kc.CreateIdentityWithPassword(ctx, traits, tempHash)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ! %s: CreateIdentityWithPassword: %v\n", u.Email, err)
		return statusCreateFailed, "", ""
	}

	if err := users.LinkKratosIdentity(ctx, u.ID, newID); err != nil {
		// Relink failed — roll back the Kratos-side create so the next
		// run doesn't leave a duplicate identity for this email.
		delCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if derr := kc.DeleteIdentity(delCtx, newID); derr != nil {
			fmt.Fprintf(os.Stderr, "  ! %s: LinkKratosIdentity failed AND rollback DeleteIdentity failed (%v); orphan id=%s\n",
				u.Email, derr, newID)
		}
		fmt.Fprintf(os.Stderr, "  ! %s: LinkKratosIdentity: %v\n", u.Email, err)
		return statusLinkFailed, "", ""
	}

	rc, err := kc.CreateRecoveryCode(ctx, newID, expiresIn)
	if err != nil {
		// Relink succeeded but the code endpoint didn't. User is in a
		// safe state (new identity + panel row relinked); operator just
		// needs to re-run recovery for this identity later. Don't roll
		// back — that would put the panel row back to a dangling UUID.
		fmt.Fprintf(os.Stderr, "  ! %s: CreateRecoveryCode: %v (user is relinked; regenerate manually)\n", u.Email, err)
		return statusRecoveryMissing, newID, ""
	}
	return statusOK, newID, rc.RecoveryLink
}

// genTempBcrypt generates a random 32-byte token, bcrypt-hashes it at cost 12
// (same as the rest of the panel), and returns the hash. The plaintext is
// discarded — the user will reset their password via the recovery link, so
// there's no need to surface the temp password anywhere.
func genTempBcrypt() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("rand.Read: %w", err)
	}
	token := hex.EncodeToString(raw) // 64 hex chars, well below bcrypt's 72-byte cap
	h, err := bcrypt.GenerateFromPassword([]byte(token), 12)
	if err != nil {
		return "", fmt.Errorf("bcrypt: %w", err)
	}
	return string(h), nil
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func derefOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}
