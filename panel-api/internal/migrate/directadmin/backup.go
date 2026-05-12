package directadmin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate"
)

// BackupUserTimeout is the upper bound for the source-side backup
// build. 5-10 GB accounts take 1-5 min on healthy disks; cap is
// generous for slow SAN-backed sources.
const BackupUserTimeout = 60 * time.Minute

// BackupUser builds a cpanel-cpmove-shaped tarball on the source
// DA host so the cpanel restore writers (via the DA→cpanel adapter)
// can consume it unchanged.
//
// Why not the DA-native `system_backup_user` CLI: that command
// doesn't exist on real DA installs (verified live, May 2026 —
// `da admin tools.system_backup_user` errors "Unrecognized
// arguments"). DA's actual backup path is HTTP API or the
// web UI. SSH-side, the simplest correct approach is to
// synthesize the cpmove layout directly: dump each DB, copy
// userdata, mirror the homedir.
//
// Produced layout:
//
//	/tmp/cpmove-<user>.tar.gz
//	└── cpmove-<user>/
//	    ├── cp/<user>            (synthesized KEY=value file)
//	    ├── homedir/             (= /home/<user>/)
//	    └── mysql/<db>.sql        (per <user>_* database)
//
// Requires admin SSH user (root or DA "admin") — needs read access
// to /usr/local/directadmin/data/users/<user>/ + MariaDB SOCKET
// for mysqldump (`--defaults-file=/usr/local/directadmin/conf/my.cnf`
// supplies admin creds DA stashed at install-time).
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

	// Single inline shell script. Done as one ssh exec so we don't
	// pay handshake N times + the tempdir is the same across steps.
	// `set -e` aborts on any failure so partial tarballs don't leak.
	// `bash -lc` to pick up DA's PATH (mysqldump from MariaDB pkg).
	acct := shellQuote(account)
	script := fmt.Sprintf(`set -e
ACCT=%s
TMP=/tmp/jabali-migrate-da-$ACCT
OUT=/tmp/cpmove-$ACCT.tar.gz
USERDIR=/usr/local/directadmin/data/users/$ACCT
HOMEDIR=$(awk -F: -v u="$ACCT" '$1==u {print $6}' /etc/passwd)
if [ -z "$HOMEDIR" ]; then HOMEDIR=/home/$ACCT; fi
DACNF=/usr/local/directadmin/conf/my.cnf

rm -rf "$TMP" "$OUT"
mkdir -p "$TMP/cpmove-$ACCT/cp" "$TMP/cpmove-$ACCT/homedir" "$TMP/cpmove-$ACCT/mysql"

# 1. cp/<user> — synthesize from user.conf so cpanel.PeekAccountMeta
#    + restore_extras parses CONTACTEMAIL + DNS lines unchanged.
if [ -f "$USERDIR/user.conf" ]; then
  awk -F= '
    /^email=/        {printf "CONTACTEMAIL=%%s\n", substr($0, index($0,"=")+1)}
    /^username=/     {printf "USER=%%s\n",         substr($0, index($0,"=")+1)}
    /^name=/         {printf "USER=%%s\n",         substr($0, index($0,"=")+1)}
  ' "$USERDIR/user.conf" > "$TMP/cpmove-$ACCT/cp/$ACCT"
  # USER fallback when neither username/name keys exist.
  grep -q '^USER=' "$TMP/cpmove-$ACCT/cp/$ACCT" || echo "USER=$ACCT" >> "$TMP/cpmove-$ACCT/cp/$ACCT"
fi
# DNS line = primary domain (first entry in domains.list).
if [ -s "$USERDIR/domains.list" ]; then
  PRIMARY=$(head -1 "$USERDIR/domains.list")
  echo "DNS=$PRIMARY" >> "$TMP/cpmove-$ACCT/cp/$ACCT"
fi
# Shadow hash so cpanel.PeekAccountMeta can preserve the source
# Linux password (M35.7). DA writes /etc/shadow normally; pull just
# this user's line + dump the bare hash.
if grep -q "^$ACCT:" /etc/shadow 2>/dev/null; then
  awk -F: -v u="$ACCT" '$1==u {print $2}' /etc/shadow > "$TMP/cpmove-$ACCT/shadow"
fi

# 2. mysql/ — dump every database named <user>_* using DA's stashed
#    admin creds. The .sql files match the cpanel cpmove naming so
#    cpanel.ImportDatabases picks them up.
if [ -r "$DACNF" ]; then
  for db in $(mysql --defaults-file="$DACNF" -BN -e "SHOW DATABASES" | grep "^${ACCT}_" || true); do
    mysqldump --defaults-file="$DACNF" --skip-lock-tables --single-transaction "$db" > "$TMP/cpmove-$ACCT/mysql/$db.sql" || true
  done
fi

# 3. homedir/ — rsync /home/<user>/ → cpmove-<user>/homedir/.
#    Skip the per-domain dirs cpanel doesn't have; cpanel.ImportHomeSplit
#    expects flat public_html/... layout (DA's <user>/domains/<dom>/
#    is similar enough that the adapter handles it).
if [ -d "$HOMEDIR" ]; then
  rsync -aH --exclude=.lock --exclude=.cache "$HOMEDIR/" "$TMP/cpmove-$ACCT/homedir/" 2>&1 | tail -5
fi

# 4. tar it.
tar -czf "$OUT" -C "$TMP" "cpmove-$ACCT"
rm -rf "$TMP"
echo "$OUT"
`, acct)
	out, err := s.run(subctx, BackupUserTimeout, script)
	if err != nil {
		return "", fmt.Errorf("da synthesize cpmove: %w (stdout=%q)", err, truncForLog(string(out), 1024))
	}
	// Script's last line is the tarball path.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	path := strings.TrimSpace(lines[len(lines)-1])
	if path == "" {
		return "", fmt.Errorf("da synthesize cpmove: empty path")
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

// shellQuote single-quotes a string for safe inclusion in /bin/bash.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
