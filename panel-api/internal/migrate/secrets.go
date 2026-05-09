package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SecretsDir is where per-job source credentials live (per ADR-0094
// §"tracked risks"). install.sh provisions this at 0750 root:jabali.
// Files inside are 0640 root:jabali.
const SecretsDir = "/etc/jabali-panel/migration-secrets"

// WipeJobSecret deletes the per-job secrets file at SecretsDir/
// <jobID>.env. Idempotent — missing file is non-fatal.
//
// Called from the restore-stage runner on terminal state (done/
// failed/cancelled) + from admin REST cancel + from the daily
// reaper as a belt-and-braces sweep. Multiple wipes of the same
// path no-op via the os.IsNotExist branch.
//
// Refuses any jobID that isn't a clean ULID-shape token to prevent
// path traversal: strict 26-char alnum check.
func WipeJobSecret(jobID string) error {
	if !isValidULIDish(jobID) {
		return fmt.Errorf("WipeJobSecret: invalid job_id %q", jobID)
	}
	path := filepath.Join(SecretsDir, jobID+".env")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// isValidULIDish accepts the ULID alphabet (Crockford base32: 26
// upper-case alphanumerics, no I/L/O/U). Permissive enough to also
// pass tests using lowercase ULIDs; caller passes ids.NewULID()
// output which uses the canonical alphabet.
func isValidULIDish(s string) bool {
	if len(s) != 26 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		default:
			return false
		}
	}
	return true
}
