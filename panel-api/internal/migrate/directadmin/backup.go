package directadmin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate"
)

// BackupUserTimeout is the upper bound for the source-side
// `da admin tools.system_backup_user` run. DA backups for a
// 5-10 GB account take 1-5 min on healthy disks; cap is generous.
const BackupUserTimeout = 60 * time.Minute

// BackupUser invokes `da admin tools.system_backup_user <user>` on
// the source host. Requires admin principal — DA refuses non-admin
// SSH user. Returns the absolute path of the produced tarball on
// the source side.
//
// DA's canonical output path is
// /usr/local/directadmin/data/users/<user>/backups/user.<user>.<ts>.tar.gz.
// Without parsing stdout (DA minor variations) we glob for the
// newest matching file in that dir + return its path. Caller
// then PullFile's it back to /var/lib/jabali-migrations/<job-id>/.
//
// **STATUS:** Coded against documented DA admin behaviour. NOT
// validated against a live DA host. Operator running against an
// unfamiliar DA minor: file the actual stdout shape so a follow-
// up commit hardens parseDABackupOutput.
func (d *Discoverer) BackupUser(ctx context.Context, raw interface{}, account string) (string, error) {
	s, ok := raw.(*session)
	if !ok || s == nil {
		return "", errors.New("BackupUser: bad session")
	}
	if !looksLikeDAUsername(account) {
		return "", fmt.Errorf("BackupUser: invalid account %q", account)
	}

	subctx, cancel := context.WithTimeout(ctx, BackupUserTimeout)
	defer cancel()

	cmd := fmt.Sprintf("da admin tools.system_backup_user '%s' 2>&1",
		strings.ReplaceAll(account, "'", `'\''`))
	out, err := s.run(subctx, BackupUserTimeout, cmd)
	if err != nil {
		return "", fmt.Errorf("system_backup_user: %w (stdout=%q)", err, truncForLog(string(out), 1024))
	}

	// Find the produced tarball. DA writes to
	// /usr/local/directadmin/data/users/<user>/backups/. Newest
	// .tar.gz in that dir is the just-produced one.
	findCmd := fmt.Sprintf(
		"ls -t /usr/local/directadmin/data/users/'%s'/backups/*.tar.gz 2>/dev/null | head -1",
		strings.ReplaceAll(account, "'", `'\''`))
	tout, terr := s.run(subctx, d.CommandTimeout, findCmd)
	if terr != nil {
		return "", fmt.Errorf("locate tarball: %w", terr)
	}
	path := strings.TrimSpace(string(tout))
	if path == "" {
		return "", fmt.Errorf("system_backup_user produced no tarball under /usr/local/directadmin/data/users/%s/backups/", account)
	}
	return path, nil
}

// PullFile is a thin wrapper over the shared migrate.PullFileViaSSH
// helper, exposing the SSH client through the package boundary.
// Same signature shape as cpanel.Discoverer.PullFile so callers
// branching on source kind don't have to special-case args.
func (d *Discoverer) PullFile(ctx context.Context, raw interface{}, remotePath, localPath string) (int64, error) {
	s, ok := raw.(*session)
	if !ok || s == nil {
		return 0, errors.New("PullFile: bad session")
	}
	return migrate.PullFileViaSSH(ctx, s.client, remotePath, localPath)
}

// looksLikeDAUsername — DA allows lowercase + digits, max 16 chars.
// Stricter than POSIX (DA itself refuses uppercase + most punct).
func looksLikeDAUsername(s string) bool {
	if len(s) < 1 || len(s) > 16 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}

// truncForLog bounds the size of stderr/stdout we surface in error
// messages so a stuck command producing MB of output doesn't blow
// up the JSON envelope.
func truncForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}
