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

# 2. mysql/ — dump every database owned by this DA user. DA registers
#    them under /usr/local/directadmin/data/users/<u>/databases.list
#    (one db per line on old DA) OR /usr/local/directadmin/data/users/
#    <u>/databases/ (directory of per-db files on newer DA). Fall back
#    to enumerating SHOW DATABASES filtered by '^<user>_' (DA enforces
#    user_<name> prefix on customer DBs).
#
#    Credentials probe: try multiple known DA stash paths (mysql.conf
#    on newer DA, my.cnf on older), then fall back to root socket auth
#    (works when local MariaDB uses unix_socket auth — common on DA
#    boxes running as root anyway).
MYSQL_AUTH=""
for _cnf in /usr/local/directadmin/conf/mysql.conf \
            /usr/local/directadmin/conf/my.cnf; do
  if [ -r "$_cnf" ] && mysql --defaults-file="$_cnf" -BN -e "SELECT 1" >/dev/null 2>&1; then
    MYSQL_AUTH="--defaults-file=$_cnf"
    break
  fi
done
if [ -z "$MYSQL_AUTH" ] && mysql -u root -BN -e "SELECT 1" >/dev/null 2>&1; then
  MYSQL_AUTH="-u root"
fi

DBS=""
if [ -s "$USERDIR/databases.list" ]; then
  DBS=$(awk 'NF' "$USERDIR/databases.list")
fi
if [ -z "$DBS" ] && [ -d "$USERDIR/databases" ]; then
  DBS=$(find "$USERDIR/databases" -maxdepth 1 -type f -printf '%%f\n' 2>/dev/null | \
        sed -e 's/\.conf$//' -e 's/\.list$//' | awk 'NF' | sort -u)
fi
if [ -z "$DBS" ] && [ -n "$MYSQL_AUTH" ]; then
  DBS=$(mysql $MYSQL_AUTH -BN -e "SHOW DATABASES" | grep "^${ACCT}_" || true)
fi
DBS_ENUMERATED=$(echo "$DBS" | awk 'NF' | wc -l)
if [ -z "$MYSQL_AUTH" ]; then
  echo "WARN: no MariaDB credentials found (tried mysql.conf, my.cnf, root-socket) — skipping $DBS_ENUMERATED databases" >&2
fi
for db in $DBS; do
  [ -z "$db" ] && continue
  if [ -n "$MYSQL_AUTH" ]; then
    if ! mysqldump $MYSQL_AUTH --skip-lock-tables --single-transaction \
         "$db" > "$TMP/cpmove-$ACCT/mysql/$db.sql" 2>/dev/null; then
      echo "WARN: mysqldump $db failed" >&2
      rm -f "$TMP/cpmove-$ACCT/mysql/$db.sql"
    fi
  fi
done

# 3. homedir is INTENTIONALLY OMITTED from this tarball — the restore
#    stage rsyncs per-domain directly source→dest over SSH, which lets
#    rsync resume on transient failures and skips re-transfer of files
#    that already match on the destination. Bundling home into a tar
#    also forced a full re-pull on every retry.
#
#    Emit a small manifest of every domain's docroot on the source so
#    the restore stage knows what to rsync. One line per domain:
#      <domain>\t<absolute-public_html-path>
#    Restore reads this + dispatches agent.migration.rsync_remote_home
#    once per row.
if [ -s "$USERDIR/domains.list" ]; then
  while read -r DOM; do
    [ -z "$DOM" ] && continue
    DOC_ROOT="$HOMEDIR/domains/$DOM/public_html"
    if [ -d "$DOC_ROOT" ]; then
      printf '%%s\t%%s\n' "$DOM" "$DOC_ROOT" >> "$TMP/cpmove-$ACCT/domains-paths.txt"
    fi
  done < "$USERDIR/domains.list"
fi
# Still bundle ~/.ssh/authorized_keys so cpanel.ImportSSHKeys picks it
# up without a separate rsync round-trip — tiny file, no impact on
# tarball size.
if [ -r "$HOMEDIR/.ssh/authorized_keys" ]; then
  mkdir -p "$TMP/cpmove-$ACCT/homedir/.ssh"
  cp "$HOMEDIR/.ssh/authorized_keys" "$TMP/cpmove-$ACCT/homedir/.ssh/authorized_keys"
fi

# 4. cron/<user> — DA stores crontabs at /var/spool/cron/$ACCT
#    (Debian) or /var/spool/cron/crontabs/$ACCT (CentOS); copy
#    whichever exists. cpanel.ImportCron walks cp/<user>/cron/<user>.
mkdir -p "$TMP/cpmove-$ACCT/cron"
for cf in "/var/spool/cron/$ACCT" "/var/spool/cron/crontabs/$ACCT"; do
  if [ -r "$cf" ]; then
    cp "$cf" "$TMP/cpmove-$ACCT/cron/$ACCT"
    break
  fi
done

# 5. dnszones/<dom>.db — DA's BIND zones live in /var/named/<dom>.db
#    (or /etc/bind/zones/ when DA configured that way). Copy each
#    one referenced in domains.list so cpanel.ImportDomains picks
#    up the full domain list (even without contents — ImportDomains
#    only needs the .db filename to derive the domain name).
mkdir -p "$TMP/cpmove-$ACCT/dnszones"
if [ -s "$USERDIR/domains.list" ]; then
  while read -r DOM; do
    [ -z "$DOM" ] && continue
    for z in "/var/named/$DOM.db" "/etc/bind/zones/$DOM.db" "/var/named/db.$DOM"; do
      if [ -r "$z" ]; then
        cp "$z" "$TMP/cpmove-$ACCT/dnszones/$DOM.db"
        break
      fi
    done
    # If BIND zone not on disk, emit an empty stub so ImportDomains
    # still creates the panel row + nginx vhost (DNS records would
    # be re-derived from the panel's own defaults).
    [ -f "$TMP/cpmove-$ACCT/dnszones/$DOM.db" ] || touch "$TMP/cpmove-$ACCT/dnszones/$DOM.db"
  done < "$USERDIR/domains.list"
fi

# 6. SSL — DA stores per-domain cert + key at
#    /usr/local/directadmin/data/users/$ACCT/domains/$DOM.cert and
#    .key. Pack into apache_tls/<dom>/ so cpanel.ImportSSL picks
#    them up (matches cpmove apache_tls layout).
mkdir -p "$TMP/cpmove-$ACCT/apache_tls"
if [ -s "$USERDIR/domains.list" ]; then
  while read -r DOM; do
    [ -z "$DOM" ] && continue
    crt="$USERDIR/domains/$DOM.cert"
    key="$USERDIR/domains/$DOM.key"
    cab="$USERDIR/domains/$DOM.cacert"
    if [ -r "$crt" ] || [ -r "$key" ]; then
      mkdir -p "$TMP/cpmove-$ACCT/apache_tls/$DOM"
      # cpanel.ImportSSL reads 'combined' first and short-circuits on a
      # non-empty value, splitting it back into cert+key by PEM block
      # type. cat cert + key into combined so the key extraction path
      # finds the PRIVATE KEY block; also write the standalone 'key'
      # file as a fallback for any future writer that prefers it.
      if [ -r "$crt" ] && [ -r "$key" ]; then
        cat "$crt" "$key" > "$TMP/cpmove-$ACCT/apache_tls/$DOM/combined"
      elif [ -r "$crt" ]; then
        cp "$crt" "$TMP/cpmove-$ACCT/apache_tls/$DOM/combined"
      fi
      [ -r "$key" ] && cp "$key" "$TMP/cpmove-$ACCT/apache_tls/$DOM/key"
      [ -r "$cab" ] && cp "$cab" "$TMP/cpmove-$ACCT/apache_tls/$DOM/cabundle"
    fi
  done < "$USERDIR/domains.list"
fi

# 7. Mail — DA stores per-domain mail config at /etc/virtual/<dom>/.
#    Pack into etc/<dom>/ so a future cpanel.ImportMailboxes
#    enrichment can read forwarders / aliases / passwd.
mkdir -p "$TMP/cpmove-$ACCT/etc"
if [ -s "$USERDIR/domains.list" ]; then
  while read -r DOM; do
    [ -z "$DOM" ] && continue
    if [ -d "/etc/virtual/$DOM" ]; then
      mkdir -p "$TMP/cpmove-$ACCT/etc/$DOM"
      cp -a "/etc/virtual/$DOM/." "$TMP/cpmove-$ACCT/etc/$DOM/" 2>/dev/null || true
    fi
  done < "$USERDIR/domains.list"
fi

# 8. tar it.
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
