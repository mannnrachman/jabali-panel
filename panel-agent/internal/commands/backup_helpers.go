package commands

import (
	"fmt"
	"os/exec"
	"regexp"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// M30 backup-side helpers. Shared across backup_home / backup_databases /
// backup_mailboxes / backup_create / backup_restore.

// ulidRE is the agent-side ULID validator; mirror of scanIDRE in
// security_malware.go (Crockford base32, 26 chars).
var ulidRE = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// backupUsernameRE mirrors usernameRE in security_malware.go. Linux
// username constraint, used to build /home/<u> paths.
var backupUsernameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// dbNameRE matches MariaDB database names: alpha + digits + underscore,
// up to 64 chars.
var dbNameRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)

// emailLocalRE matches the local-part of an email address; we don't
// allow shell-special chars in mailbox tokens that build CLI args.
var emailLocalRE = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// emailDomainRE matches a domain label list.
var emailDomainRE = regexp.MustCompile(`^[a-zA-Z0-9.-]+$`)

// bkInvalidArg is the backup-side wrapper for InvalidArgument errors.
func bkInvalidArg(msg string) error {
	return &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: msg}
}

// bkInternal wraps an internal-error envelope.
func bkInternal(msg string, err error) error {
	return &agentwire.AgentError{
		Code:    agentwire.CodeInternal,
		Message: fmt.Sprintf("%s: %v", msg, err),
	}
}

// bkResticBin returns the restic binary path. Hard-fail in handlers
// that need it; the foundation step (install_backup_foundation) is
// supposed to land it on every host.
func bkResticBin() (string, error) {
	bin, err := exec.LookPath("restic")
	if err != nil {
		return "", fmt.Errorf("restic binary missing: %w", err)
	}
	return bin, nil
}
