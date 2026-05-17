package ssoadmin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The single security decision (ADR-0099): the admin-all-DBs sentinel
// resolves to which privileged account + which secret file, per
// engine. Both SSO validate twins call this; pre-extraction the
// decision was duplicated verbatim in sso_phpmyadmin_validate.go and
// sso_adminer_validate.go (the M46 admin-SSO block). One place now.
func TestAdminCredential(t *testing.T) {
	dir := t.TempDir()
	pma := filepath.Join(dir, "pma-admin.password")
	pg := filepath.Join(dir, "postgres.password")
	if err := os.WriteFile(pma, []byte("  pmaSecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pg, []byte("pgSecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Tests redirect the package secret paths (test-only override).
	pmaAdminPasswordPath = pma
	pgSuperPasswordPath = pg

	t.Run("mariadb → jabali_pma_admin, trimmed secret", func(t *testing.T) {
		c, err := AdminCredential("mariadb")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if c.Username != "jabali_pma_admin" || c.Password != "pmaSecret" {
			t.Fatalf("got %+v", c)
		}
	})
	t.Run("postgres → postgres superuser", func(t *testing.T) {
		c, err := AdminCredential("postgres")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if c.Username != "postgres" || c.Password != "pgSecret" {
			t.Fatalf("got %+v", c)
		}
	})
	t.Run("unknown engine rejected", func(t *testing.T) {
		if _, err := AdminCredential("oracle"); !errors.Is(err, ErrUnknownEngine) {
			t.Fatalf("want ErrUnknownEngine, got %v", err)
		}
	})
	t.Run("missing secret file → ErrSecretUnavailable", func(t *testing.T) {
		pmaAdminPasswordPath = filepath.Join(dir, "nope")
		if _, err := AdminCredential("mariadb"); !errors.Is(err, ErrSecretUnavailable) {
			t.Fatalf("want ErrSecretUnavailable, got %v", err)
		}
	})
}
