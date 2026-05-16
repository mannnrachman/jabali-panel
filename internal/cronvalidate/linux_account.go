package cronvalidate

import (
	"errors"
	"strings"
)

// ErrNoLinuxAccount is returned when a cron operation targets a user
// that has no Linux account (no provisioned username). A cron job for
// such a user can never be materialised — the reconciler would loop
// forever trying to write a systemd timer for a nonexistent user — so
// both the REST handler and the `jabali cron` CLI must reject it up
// front. Single source of the rule (ADR-0083): callers errors.Is this.
var ErrNoLinuxAccount = errors.New("cronvalidate: user has no linux account")

// ValidateLinuxAccount enforces the precondition that a cron target
// user has a provisioned Linux account. username is the user's Linux
// username (empty/whitespace = not provisioned).
func ValidateLinuxAccount(username string) error {
	if strings.TrimSpace(username) == "" {
		return ErrNoLinuxAccount
	}
	return nil
}
