package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// runKratosMigrateTOTPReport backs the `jabali kratos-migrate --totp-only` flag.
//
// Background (see docs/adr/0034 + plans/m20-kratos-identity.md §1 step 8):
// Ory Kratos does NOT accept TOTP or lookup_secret credentials via its admin
// identity-create API — the CLI explicitly documents "credential import is not
// yet supported" for anything beyond password/OIDC (kratos-identities-import
// docs, Kratos 1.x). Our backup codes are bcrypt-hashed and cannot be mapped
// into Kratos's lookup_secret format (which stores plaintext). So TOTP data
// cannot follow the user across the cutover.
//
// This subcommand therefore does NOT attempt an import. It scans the panel
// `users` table for every row with `totp_enabled = true`, counts each row's
// unused backup codes, and emits a CSV that the operator uses to notify
// affected users before cutover. Post-cutover, those users re-enroll via
// Kratos's self-service Security → Authenticator flow (documented in the
// runbook).
//
// The command is read-only: nothing in the panel DB, Kratos DB, or nginx
// config changes. Safe to run repeatedly.
func runKratosMigrateTOTPReport(ctx context.Context, opts kratosMigrateOptions) error {
	log := sharedLog
	if log == nil {
		log = slog.Default()
	}

	out, closeOut, err := openTOTPReportOutput(opts.TOTPOutput)
	if err != nil {
		return err
	}
	defer closeOut()

	var users []models.User
	if err := sharedDB.WithContext(ctx).
		Where("totp_enabled = ?", true).
		Order("email ASC").
		Find(&users).Error; err != nil {
		return fmt.Errorf("fetch totp-enabled users: %w", err)
	}

	unusedCodes, err := countUnusedBackupCodes(ctx)
	if err != nil {
		return fmt.Errorf("count backup codes: %w", err)
	}

	w := csv.NewWriter(out)
	header := []string{
		"email",
		"username",
		"panel_user_id",
		"kratos_identity_id",
		"totp_enabled_at",
		"unused_backup_codes",
		"needs_reenrollment",
	}
	if err := w.Write(header); err != nil {
		return fmt.Errorf("write csv header: %w", err)
	}

	var linked, unlinked int
	for _, u := range users {
		kratosID := ""
		if u.KratosIdentityID != nil {
			kratosID = *u.KratosIdentityID
			linked++
		} else {
			unlinked++
		}
		username := ""
		if u.Username != nil {
			username = *u.Username
		}
		enabledAt := ""
		if u.TOTPEnabledAt != nil {
			enabledAt = u.TOTPEnabledAt.UTC().Format(time.RFC3339)
		}
		row := []string{
			u.Email,
			username,
			u.ID,
			kratosID,
			enabledAt,
			strconv.Itoa(unusedCodes[u.ID]),
			"yes",
		}
		if err := w.Write(row); err != nil {
			return fmt.Errorf("write csv row for %q: %w", u.Email, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("flush csv: %w", err)
	}

	log.Info("totp-only report complete",
		"affected_users", len(users),
		"linked_to_kratos", linked,
		"unlinked", unlinked,
		"output", opts.TOTPOutput)

	if unlinked > 0 {
		log.Warn("some TOTP-enabled users have no kratos_identity_id — run the password migration first so the report can correlate panel rows with Kratos identities",
			"unlinked_count", unlinked)
	}
	return nil
}

// openTOTPReportOutput resolves --totp-output to an io.Writer. Empty path or
// "-" writes to stdout. Any other path truncates-and-creates the file. The
// returned closer is always safe to call (it's a no-op for stdout).
func openTOTPReportOutput(path string) (io.Writer, func(), error) {
	if path == "" || path == "-" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open totp report output %q: %w", path, err)
	}
	return f, func() { _ = f.Close() }, nil
}

// countUnusedBackupCodes returns a map panel-user-id → count-of-unused codes.
// Single grouped query so the walk of the users slice stays O(n).
func countUnusedBackupCodes(ctx context.Context) (map[string]int, error) {
	type row struct {
		UserID string
		N      int
	}
	var rows []row
	if err := sharedDB.WithContext(ctx).
		Table("totp_backup_codes").
		Select("user_id AS user_id, COUNT(*) AS n").
		Where("used_at IS NULL").
		Group("user_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.UserID] = r.N
	}
	return out, nil
}
