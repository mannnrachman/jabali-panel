// jabali admin disable-2fa --email <target> — break-glass escape hatch
// for users who have lost their authenticator device AND all 10 backup
// codes. Wipes totp_secret_encrypted, totp_enabled=false, and deletes
// all totp_backup_codes rows for the user. User can re-enrol from the
// UI after the next login.
//
// This is the last-resort path. No API endpoint equivalent exists —
// requiring shell access on the panel host means an attacker who can
// run this already owns the box.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func newAdminDisable2FACmd() *cobra.Command {
	var email string

	cmd := &cobra.Command{
		Use:   "disable-2fa",
		Short: "Break-glass: clear TOTP + backup codes for a user",
		Long: `Clear the target user's TOTP secret, totp_enabled flag, and all
backup codes. Use when a user has lost their authenticator device AND
their 10 backup codes. The user can re-enrol from the UI on next login.

Audit-logged; no remote/API equivalent by design.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := initConfig(); err != nil {
				return err
			}
			if err := initDB(); err != nil {
				return err
			}
			return runAdminDisable2FA(cmd.Context(), sharedDB, sharedLog, email)
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "target user email (required)")
	_ = cmd.MarkFlagRequired("email")
	return cmd
}

func runAdminDisable2FA(ctx context.Context, db *gorm.DB, log *slog.Logger, email string) error {
	users := repository.NewUserRepository(db)
	backups := repository.NewTOTPBackupCodeRepository(db)
	return disable2FA(ctx, users, backups, log, email)
}

// disable2FA is the logic split out from the cobra wrapper so it can be
// tested without a gorm.DB fixture. Takes repo interfaces directly.
func disable2FA(
	ctx context.Context,
	users repository.UserRepository,
	backups repository.TOTPBackupCodeRepository,
	log *slog.Logger,
	email string,
) error {
	u, err := users.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("no user with email %q", email)
		}
		return fmt.Errorf("find user: %w", err)
	}

	if !u.TOTPEnabled && u.TOTPSecretEncrypted == nil {
		fmt.Printf("%s — 2FA already disabled, nothing to do\n", email)
		return nil
	}

	// Order matters. Backup codes first — if DisableTOTP succeeds but code
	// cleanup fails, the user is already unlocked (totp_enabled=false) and
	// stale codes are harmless (orphan rows, no user can authenticate with
	// them because there's no secret). The reverse order leaves the user
	// locked out if the second step fails.
	if err := backups.DeleteAllByUserID(ctx, u.ID); err != nil {
		return fmt.Errorf("delete backup codes: %w", err)
	}
	if err := users.DisableTOTP(ctx, u.ID); err != nil {
		return fmt.Errorf("disable totp: %w", err)
	}

	log.Info("event=audit kind=admin_disable_2fa actor=cli target_id=" + u.ID + " target_email=" + u.Email)
	fmt.Printf("2FA disabled for %s (user %s). They can re-enrol on next login.\n", u.Email, u.ID)
	return nil
}
