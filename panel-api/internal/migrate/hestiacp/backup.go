package hestiacp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate"
)

// BackupUserTimeout caps `v-backup-user` runtime. Hestia full-account
// backup of a 5 GB account: 30 sec - 5 min healthy. 60-min ceiling
// matches DA + cpanel for symmetry.
const BackupUserTimeout = 60 * time.Minute

// BackupUser invokes `v-backup-user <user>` on the source. Requires
// root SSH user. Returns the absolute path of the produced tar on
// the source.
//
// Hestia writes to /backup/<user>.<timestamp>.tar (or .tar.gz
// depending on /usr/local/hestia/conf/hestia.conf BACKUP_GZIP). We
// glob for the newest matching file rather than parse stdout —
// v-backup-user output format varies across minors.
//
// **STATUS:** Coded against documented Hestia behaviour. NOT
// validated against a live Hestia host. File field-set drift via
// the manifest's Warnings list when this lands against an
// unfamiliar Hestia minor.
func (d *Discoverer) BackupUser(ctx context.Context, raw interface{}, account string) (string, error) {
	s, ok := raw.(*session)
	if !ok || s == nil {
		return "", errors.New("BackupUser: bad session")
	}
	if account == "" {
		return "", errors.New("BackupUser: account empty")
	}

	subctx, cancel := context.WithTimeout(ctx, BackupUserTimeout)
	defer cancel()

	cmd := fmt.Sprintf("v-backup-user '%s' 2>&1",
		strings.ReplaceAll(account, "'", `'\''`))
	out, err := s.run(subctx, BackupUserTimeout, cmd)
	if err != nil {
		return "", fmt.Errorf("v-backup-user: %w (stdout=%q)", err, truncForLog(string(out), 1024))
	}

	// Find newest backup tar for this user. Hestia BACKUP_PATH
	// defaults to /backup; some operator overrides set it
	// elsewhere via /usr/local/hestia/conf/hestia.conf. v-backup-user
	// itself prints the path when verbose; for v1 we trust the
	// canonical /backup/ location.
	findCmd := fmt.Sprintf(
		"ls -t /backup/'%s'.*.tar /backup/'%s'.*.tar.gz 2>/dev/null | head -1",
		strings.ReplaceAll(account, "'", `'\''`),
		strings.ReplaceAll(account, "'", `'\''`))
	tout, terr := s.run(subctx, d.CommandTimeout, findCmd)
	if terr != nil {
		return "", fmt.Errorf("locate hestia tar: %w", terr)
	}
	path := strings.TrimSpace(string(tout))
	if path == "" {
		return "", fmt.Errorf("v-backup-user produced no tar under /backup/%s.*.{tar,tar.gz}", account)
	}
	return path, nil
}

// PullFile = shared SSH-cat-stream over the session's *ssh.Client.
func (d *Discoverer) PullFile(ctx context.Context, raw interface{}, remotePath, localPath string) (int64, error) {
	s, ok := raw.(*session)
	if !ok || s == nil {
		return 0, errors.New("PullFile: bad session")
	}
	return migrate.PullFileViaSSH(ctx, s.client, remotePath, localPath)
}

// truncForLog bounds error-message size so a stuck v-backup-user
// producing MB of output doesn't blow up the JSON envelope.
func truncForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}
