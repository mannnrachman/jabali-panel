package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
)

// newSSOCmd creates the 'jabali sso' subcommand group.
func newSSOCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sso",
		Short: "SSO (Single Sign-On) management commands",
		Long:  "Manage phpMyAdmin SSO key rotation and token cleanup.",
	}

	cmd.AddCommand(newSSORotateKeyCmd())
	cmd.AddCommand(newSSOPruneTokensCmd())

	return cmd
}

// newSSORotateKeyCmd creates the 'jabali sso rotate-key' subcommand.
func newSSORotateKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate-key",
		Short: "Rotate the SSO encryption key",
		Long: `Rotate the AES-256-GCM key used to encrypt database-user passwords.

This command:
1. Loads the current key and the new key from disk
2. Opens a database transaction
3. Decrypts all shadow-account passwords with the current key
4. Re-encrypts each password with the new key
5. Updates the database
6. Commits the transaction (or rolls back on any error)

If successful, the database is updated but the filesystem key file is NOT
changed — you must manually swap the key files and reload the service.

Example:
  openssl rand 32 | base64 > /tmp/new_sso_key.txt
  jabali sso rotate-key \
    --current-key /etc/jabali/sso_key.txt \
    --new-key /tmp/new_sso_key.txt
  mv /tmp/new_sso_key.txt /etc/jabali/sso_key.txt
  systemctl kill -s SIGHUP jabali-panel
`,
		RunE: runSSORotateKey,
	}

	cmd.Flags().String("current-key", "/etc/jabali/sso_key.txt", "path to current encryption key")
	cmd.Flags().String("new-key", "", "path to new encryption key (required)")
	cmd.MarkFlagRequired("new-key")

	return cmd
}

// runSSORotateKey executes the key rotation transaction.
func runSSORotateKey(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// Load config
	if err := initConfig(); err != nil {
		return err
	}

	// Load database
	if err := initDB(); err != nil {
		return err
	}

	currentKeyPath, _ := cmd.Flags().GetString("current-key")
	newKeyPath, _ := cmd.Flags().GetString("new-key")

	// Load keys
	currentKey, err := ssokey.Load(currentKeyPath)
	if err != nil {
		return fmt.Errorf("load current key: %w", err)
	}

	newKey, err := ssokey.Load(newKeyPath)
	if err != nil {
		return fmt.Errorf("load new key: %w", err)
	}

	userRepo := repository.NewUserRepository(sharedDB)

	// Execute rotation in a transaction
	count, err := rotateSSOKeys(ctx, sharedLog, sharedDB, userRepo, &currentKey, &newKey)
	if err != nil {
		return fmt.Errorf("rotate keys: %w", err)
	}

	fmt.Printf("Successfully rotated %d shadow-account password(s)\n", count)
	return nil
}

// rotateSSOKeys decrypts all shadow-account passwords with currentKey and
// re-encrypts them with newKey in a single transaction.
func rotateSSOKeys(
	ctx context.Context,
	log *slog.Logger,
	gdb *gorm.DB,
	userRepo repository.UserRepository,
	currentKey, newKey *ssokey.Key,
) (int, error) {
	// Begin transaction
	tx := gdb.WithContext(ctx).Begin()
	if tx.Error != nil {
		return 0, fmt.Errorf("begin transaction: %w", tx.Error)
	}

	// Fetch all users with encrypted passwords
	var users []models.User
	if err := tx.Where("mysqladmin_password_enc IS NOT NULL").Find(&users).Error; err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("fetch users: %w", err)
	}

	// Rotate each password
	updated := 0
	for _, user := range users {
		if user.MysqladminPasswordEnc == nil {
			continue
		}

		// Decrypt with current key
		plaintextBytes, err := currentKey.Open(user.MysqladminPasswordEnc)
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("decrypt password for user %q: %w", user.ID, err)
		}

		// Re-encrypt with new key
		newEnc, err := newKey.Seal(plaintextBytes)
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("encrypt password for user %q: %w", user.ID, err)
		}

		// Update user
		if err := tx.Model(&user).Update("mysqladmin_password_enc", newEnc).Error; err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("update user %q: %w", user.ID, err)
		}

		updated++
	}

	// Commit
	if err := tx.Commit().Error; err != nil {
		return 0, fmt.Errorf("commit transaction: %w", err)
	}

	log.InfoContext(ctx, "sso key rotation completed", "users_updated", updated)
	return updated, nil
}

// newSSOPruneTokensCmd creates the 'jabali sso prune-tokens' subcommand.
func newSSOPruneTokensCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune-tokens",
		Short: "Manually purge expired SSO tokens",
		Long: `Purge expired phpMyAdmin SSO tokens from the database.

Normally, the reconciler automatically calls PurgeExpired() every 5 minutes
during the nightly maintenance window. This command forces an immediate purge
if needed.

Example:
  jabali sso prune-tokens
`,
		RunE: runSSOPruneTokens,
	}

	return cmd
}

// runSSOPruneTokens executes the token prune.
func runSSOPruneTokens(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// Load config
	if err := initConfig(); err != nil {
		return err
	}

	// Load database
	if err := initDB(); err != nil {
		return err
	}

	tokenRepo := repository.NewPhpMyAdminSSOTokenRepository(sharedDB)
	count, err := tokenRepo.PurgeExpired(ctx)
	if err != nil {
		return fmt.Errorf("purge expired tokens: %w", err)
	}

	fmt.Printf("Purged %d expired token(s)\n", count)
	return nil
}
