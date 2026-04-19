package commands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestComputeDokuWikiInstallPath(t *testing.T) {
	cases := []struct {
		docroot string
		subdir  string
		want    string
	}{
		{"/home/alice/domains/x/public_html", "", "/home/alice/domains/x/public_html"},
		{"/home/alice/domains/x/public_html", "wiki", "/home/alice/domains/x/public_html/wiki"},
		// computeDokuWikiInstallPath does no boundary check — that's
		// validateDocrootPath's job. Joining a relative subdir is a
		// pure filepath.Join, so escape attempts surface in the
		// validator at the API layer, not here.
	}
	for _, c := range cases {
		got := computeDokuWikiInstallPath(c.docroot, c.subdir)
		if got != c.want {
			t.Errorf("computeDokuWikiInstallPath(%q, %q) = %q, want %q", c.docroot, c.subdir, got, c.want)
		}
	}
}

func TestPhpEscapeSingleQuoted(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"with 'quote'", `with \'quote\'`},
		{`back\slash`, `back\\slash`},
		{`mix \ and '`, `mix \\ and \'`},
	}
	for _, c := range cases {
		got := phpEscapeSingleQuoted(c.in)
		if got != c.want {
			t.Errorf("phpEscapeSingleQuoted(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildDokuWikiLocalPHP_BasicFields(t *testing.T) {
	out := buildDokuWikiLocalPHP("My Wiki", "cc-by-sa")
	checks := []string{
		"<?php",
		"$conf['title'] = 'My Wiki';",
		"$conf['license'] = 'cc-by-sa';",
		"$conf['license_url'] = 'https://creativecommons.org/licenses/by-sa/4.0/';",
		"$conf['useacl'] = 1;",
		"$conf['superuser'] = '@admin';",
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("expected output to contain %q, got:\n%s", c, out)
		}
	}
}

func TestBuildDokuWikiLocalPHP_LicenseNoneOmitsURL(t *testing.T) {
	out := buildDokuWikiLocalPHP("X", "none")
	if strings.Contains(out, "license_url") {
		t.Errorf("license=none should not write license_url; got:\n%s", out)
	}
	if !strings.Contains(out, "$conf['license'] = 'none';") {
		t.Errorf("expected license='none' line, got:\n%s", out)
	}
}

func TestBuildDokuWikiLocalPHP_TitleQuoteSafe(t *testing.T) {
	out := buildDokuWikiLocalPHP("It's mine", "cc-by-sa")
	want := `$conf['title'] = 'It\'s mine';`
	if !strings.Contains(out, want) {
		t.Errorf("expected %q, got:\n%s", want, out)
	}
}

func TestBuildDokuWikiUsersAuth_Format(t *testing.T) {
	out := buildDokuWikiUsersAuth("alice", "$2y$10$abcdef", "alice@example.com")
	if !strings.Contains(out, "alice:$2y$10$abcdef:Administrator:alice@example.com:admin,user") {
		t.Errorf("users.auth.php has wrong format:\n%s", out)
	}
}

func TestBuildDokuWikiACLAuth_GrantsAdmin(t *testing.T) {
	out := buildDokuWikiACLAuth("alice")
	wants := []string{
		"*\t@ALL\t1",
		"*\t@user\t8",
		"*\t@admin\t16",
		"alice\t@admin\t16",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("expected %q in acl.auth.php, got:\n%s", w, out)
		}
	}
}

func TestVerifyDokuWikiSHA256_EmptyPinIsPermissive(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "tarball")
	if err := os.WriteFile(tmp, []byte("not a real tarball"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dokuwikiTarballSHA256 != "" {
		t.Skip("dokuwikiTarballSHA256 is non-empty in this build; permissive-pin behaviour cannot be exercised")
	}
	if err := verifyDokuWikiSHA256(tmp); err != nil {
		t.Fatalf("empty pin should accept any file, got: %v", err)
	}
}

func TestDokuwikiInstallHandler_RejectsBadJSON(t *testing.T) {
	_, err := dokuwikiInstallHandler(context.Background(), json.RawMessage("not-json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("want *agentwire.AgentError, got %T", err)
	}
	if ae.Code != agentwire.CodeInvalidArgument {
		t.Errorf("code = %q", ae.Code)
	}
}

func TestDokuwikiInstallHandler_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing os_user", `{"docroot":"/home/x/domains/y/public_html","admin_user":"a","admin_pass":"p","admin_email":"a@b.c","license":"cc-by-sa"}`},
		{"missing docroot", `{"os_user":"alice","admin_user":"a","admin_pass":"p","admin_email":"a@b.c","license":"cc-by-sa"}`},
		{"missing admin_user", `{"os_user":"alice","docroot":"/home/alice/domains/y/public_html","admin_pass":"p","admin_email":"a@b.c","license":"cc-by-sa"}`},
		{"missing admin_pass", `{"os_user":"alice","docroot":"/home/alice/domains/y/public_html","admin_user":"a","admin_email":"a@b.c","license":"cc-by-sa"}`},
		{"missing admin_email", `{"os_user":"alice","docroot":"/home/alice/domains/y/public_html","admin_user":"a","admin_pass":"p","license":"cc-by-sa"}`},
		{"unknown license", `{"os_user":"alice","docroot":"/home/alice/domains/y/public_html","admin_user":"a","admin_pass":"p","admin_email":"a@b.c","license":"funky"}`},
		{"docroot escape", `{"os_user":"alice","docroot":"/etc","admin_user":"a","admin_pass":"p","admin_email":"a@b.c","license":"cc-by-sa"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := dokuwikiInstallHandler(context.Background(), json.RawMessage(c.body))
			if err == nil {
				t.Fatalf("expected error for %q, got nil", c.name)
			}
			var ae *agentwire.AgentError
			if !errors.As(err, &ae) {
				t.Fatalf("want *agentwire.AgentError, got %T", err)
			}
			if ae.Code != agentwire.CodeInvalidArgument {
				t.Errorf("code = %q (want %q)", ae.Code, agentwire.CodeInvalidArgument)
			}
		})
	}
}

func TestDokuWikiAppDescriptor_RegisteredOnInstaller(t *testing.T) {
	// Smoke test: app_dispatch.go has a wordpress installer registered
	// from package init; this verifies dokuwiki landed there too. The
	// dispatcher tests cover the broader contract.
	appDispatchMu.RLock()
	_, ok := appInstallers["dokuwiki"]
	appDispatchMu.RUnlock()
	if !ok {
		t.Fatal("dokuwiki installer not registered on appInstallers")
	}
	appDispatchMu.RLock()
	_, ok = appDeleters["dokuwiki"]
	appDispatchMu.RUnlock()
	if !ok {
		t.Fatal("dokuwiki deleter not registered on appDeleters")
	}
}
