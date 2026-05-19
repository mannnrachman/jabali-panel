package cpanel

import (
	"os"
	"path/filepath"
	"testing"
)

// Real-shape sample from a cPanel-emitted cpmove-<u>/mysql.sql.
// Two hosts (localhost + a public IP + a hostname) for the SAME user,
// then a second user with one host. Verifies:
//   - only @localhost is kept (the migrated app connects locally)
//   - hash is captured (mysql_native_password 41-char)
//   - per-DB grants attach to the right user
//   - `\_` underscore-escape on DB names is stripped
//   - "ALL PRIVILEGES" normalises to ["ALL"]
const sampleGrants = `-- header
GRANT USAGE ON *.* TO 'newpuzzl_wp'@'182.54.236.170' IDENTIFIED BY PASSWORD '*E5CC8DDDA4EE46455C6D5132C3B67DC123B7A645';
GRANT ALL PRIVILEGES ON ` + "`newpuzzleans\\_newpuzzl\\_wp`" + `.* TO 'newpuzzl_wp'@'182.54.236.170';
GRANT USAGE ON *.* TO 'newpuzzl_wp'@'localhost' IDENTIFIED BY PASSWORD '*E5CC8DDDA4EE46455C6D5132C3B67DC123B7A645';
GRANT ALL PRIVILEGES ON ` + "`newpuzzleans\\_newpuzzl\\_wp`" + `.* TO 'newpuzzl_wp'@'localhost';
GRANT USAGE ON *.* TO 'newpuzzleans'@'localhost' IDENTIFIED BY PASSWORD '*3C7ABC0001A1B3A38252B2DF99EBB210BB9F6EDD';
GRANT SELECT, INSERT ON ` + "`newpuzzleans\\_newpuzzl\\_wp`" + `.* TO 'newpuzzleans'@'localhost';
`

func TestParseMySQLGrants_RealShape(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "mysql.sql")
	if err := os.WriteFile(path, []byte(sampleGrants), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	users, err := ParseMySQLGrants(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Two users (@localhost only — the IP host entry is filtered).
	if len(users) != 2 {
		t.Fatalf("want 2 users (@localhost), got %d: %+v", len(users), users)
	}
	byName := map[string]CompatUser{}
	for _, u := range users {
		if u.Host != "localhost" {
			t.Errorf("non-localhost user leaked through: %+v", u)
		}
		if !IsNativePasswordHash(u.Hash) {
			t.Errorf("user %s: hash %q not mysql_native_password shape", u.Name, u.Hash)
		}
		byName[u.Name] = u
	}
	u := byName["newpuzzl_wp"]
	if u.Hash != "*E5CC8DDDA4EE46455C6D5132C3B67DC123B7A645" {
		t.Errorf("newpuzzl_wp hash = %q", u.Hash)
	}
	if len(u.Grant) != 1 ||
		u.Grant[0].SourceDB != "newpuzzleans_newpuzzl_wp" ||
		len(u.Grant[0].Privs) != 1 || u.Grant[0].Privs[0] != "ALL" {
		t.Errorf("newpuzzl_wp grants = %+v", u.Grant)
	}
	u2 := byName["newpuzzleans"]
	if len(u2.Grant) != 1 || u2.Grant[0].Privs[0] != "SELECT" || u2.Grant[0].Privs[1] != "INSERT" {
		t.Errorf("newpuzzleans grants = %+v", u2.Grant)
	}
}

func TestParseMySQLGrants_MissingFileIsNotAnError(t *testing.T) {
	users, err := ParseMySQLGrants(filepath.Join(t.TempDir(), "absent.sql"))
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if users != nil {
		t.Errorf("missing file should return nil, got: %+v", users)
	}
}

func TestIsNativePasswordHash(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"*E5CC8DDDA4EE46455C6D5132C3B67DC123B7A645", true},
		{"*abcdef0123456789ABCDEF0123456789ABCDEF01", true},
		{"E5CC8DDDA4EE46455C6D5132C3B67DC123B7A645", false}, // no *
		{"*tooShort", false},
		{"*ZZCC8DDDA4EE46455C6D5132C3B67DC123B7A645", false}, // non-hex
		{"", false},
	}
	for _, c := range cases {
		if got := IsNativePasswordHash(c.in); got != c.want {
			t.Errorf("IsNativePasswordHash(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
