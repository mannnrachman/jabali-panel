package commands

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
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

// bkResticConfig builds a ResticConfig pointing at the destination
// (post-M30.2 / ADR-0080). repoURL empty falls back to the legacy
// local repo so unit tests + callers that don't supply a destination
// keep working unchanged.
func bkResticConfig(repoURL, credentialsRef string, extraOptions []string) (backup.ResticConfig, error) {
	return bkResticConfigWithPassword(repoURL, credentialsRef, "", extraOptions)
}

// bkResticConfigWithPassword is the variant used by interactive
// disaster-recovery. passwordFile empty falls back to the canonical
// /etc/jabali-panel/restic-repo.password; non-empty lets the CLI hand
// a temp-file path so a live host's runtime password isn't clobbered
// during a drill / test recovery.
func bkResticConfigWithPassword(repoURL, credentialsRef, passwordFile string, extraOptions []string) (backup.ResticConfig, error) {
	cfg := backup.DefaultConfig()
	if repoURL != "" {
		cfg.Repo = repoURL
	}
	if passwordFile != "" {
		cfg.PasswordFile = passwordFile
	}
	if len(extraOptions) > 0 {
		cfg.ExtraOptions = append([]string{}, extraOptions...)
	}
	if credentialsRef != "" {
		env, err := backup.LoadEnvFile(credentialsRef)
		if err != nil {
			return cfg, fmt.Errorf("load creds %s: %w", credentialsRef, err)
		}
		cfg.ExtraEnv = env
	}
	return cfg, nil
}

// bkEnsureRepoReady probes the remote and runs mkdir -p (SFTP only) +
// `restic init` if the repo doesn't exist yet. Idempotent — succeeds
// on already-initialized repos. Local destinations get the parent dir
// created if missing; failures bubble up.
func bkEnsureRepoReady(ctx context.Context, repoURL, credentialsRef, destKind string, sftp *backupSFTPInputs, extraOptions []string) error {
	if repoURL == "" {
		return nil
	}
	var extraEnv []string
	if credentialsRef != "" {
		env, err := backup.LoadEnvFile(credentialsRef)
		if err != nil {
			return fmt.Errorf("load creds: %w", err)
		}
		extraEnv = env
	}
	_, snapStderr, snapErr := backup.SnapshotsRemote(ctx, nil, repoURL, backup.DefaultPasswordFile, extraEnv, extraOptions)
	if snapErr == nil {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(string(snapStderr)))
	if !strings.Contains(lower, "repository does not exist") &&
		!strings.Contains(lower, "unable to open config file") {
		// Not a missing-repo signal — surface up.
		return fmt.Errorf("snapshots probe: %w (stderr: %s)", snapErr, lower)
	}
	if destKind == "sftp" && sftp != nil && sftp.Host != "" {
		if _, err := backup.MkdirRemoteSFTP(ctx, backup.SFTPInputs{
			Host:    sftp.Host,
			User:    sftp.User,
			Port:    sftp.Port,
			Path:    sftp.Path,
			Auth:    sftp.Auth,
			KeyPath: sftp.KeyPath,
		}, extraEnv); err != nil {
			return fmt.Errorf("ssh mkdir: %w", err)
		}
	}
	_, initStderr, initErr := backup.InitRemote(ctx, nil, repoURL, backup.DefaultPasswordFile, extraEnv, extraOptions)
	if initErr != nil {
		ls := strings.ToLower(strings.TrimSpace(string(initStderr)))
		if strings.Contains(ls, "already initialized") ||
			strings.Contains(ls, "config file already exists") {
			return nil
		}
		return fmt.Errorf("restic init: %w (stderr: %s)", initErr, ls)
	}
	return nil
}
