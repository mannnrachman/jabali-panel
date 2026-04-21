package commands

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const (
	// 43 'A's — valid base64url alphabet, valid length, deterministic.
	validNonce = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	// ULID spec example.
	validULID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
)

func TestGenerateNonce(t *testing.T) {
	n1, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce: %v", err)
	}
	if got := len(n1); got != 43 {
		t.Errorf("len = %d, want 43", got)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(n1) {
		t.Errorf("nonce %q not in base64url alphabet", n1)
	}
	n2, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce (2nd): %v", err)
	}
	if n1 == n2 {
		t.Errorf("two consecutive GenerateNonce calls returned identical value")
	}
}

func TestRenderSSOTemplate_HappyPath(t *testing.T) {
	out, err := RenderSSOTemplate(validNonce, "/var/www/html/wp-load.php", validULID, "admin")
	if err != nil {
		t.Fatalf("RenderSSOTemplate: %v", err)
	}
	if !strings.Contains(out, validNonce) {
		t.Errorf("output missing nonce")
	}
	if !strings.Contains(out, validULID) {
		t.Errorf("output missing install id")
	}
	if !strings.Contains(out, "$admin_username = 'admin';") {
		t.Errorf("output missing admin_username literal: %s", excerpt(out, "$admin_username"))
	}
	if !strings.Contains(out, "'/var/www/html/wp-load.php'") {
		t.Errorf("output missing single-quoted wp-load.php path")
	}
	if !strings.Contains(out, "60 < time()") {
		t.Errorf("output missing TTL_SECONDS substitution: %s", excerpt(out, "filemtime"))
	}
	if regexp.MustCompile(`__JABALI_[A-Z_]+__`).MatchString(out) {
		t.Errorf("output still contains a __MARKER__ — leftover substitution")
	}
}

// TestRenderSSOTemplate_GuardsAllSpeculativeFetches is a regression guard
// for the 2026-04-21 field incident: the template's prefetch guard used
// strict equality `$secPurpose === 'prefetch'` and missed Chrome's
// `Sec-Purpose: prefetch;prerender` header. A Chrome prerender silently
// consumed the SSO file (302 + unlink) before the user's real click,
// which then 404'd. The fix treats any non-empty Sec-Purpose as
// speculative, per the Fetch Metadata spec.
func TestRenderSSOTemplate_GuardsAllSpeculativeFetches(t *testing.T) {
	out, err := RenderSSOTemplate(validNonce, "/var/www/html/wp-load.php", validULID, "admin")
	if err != nil {
		t.Fatalf("RenderSSOTemplate: %v", err)
	}
	if !strings.Contains(out, "$secPurpose !== ''") {
		t.Errorf("template missing forward-compatible Sec-Purpose guard " +
			"($secPurpose !== ''). Reverting to '=== prefetch' misses " +
			"Chrome's 'prefetch;prerender' and lets prerender consume the file.")
	}
	if strings.Contains(out, "$secPurpose === 'prefetch'") {
		t.Errorf("template uses strict Sec-Purpose equality — misses prerender")
	}
}

func TestRenderSSOTemplate_RejectsInvalidNonce(t *testing.T) {
	cases := []struct {
		name, nonce string
	}{
		{"empty", ""},
		{"42 chars", strings.Repeat("A", 42)},
		{"44 chars", strings.Repeat("A", 44)},
		{"plus (stdlib base64)", strings.Repeat("A", 42) + "+"},
		{"slash (stdlib base64)", strings.Repeat("A", 42) + "/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := RenderSSOTemplate(c.nonce, "/var/www/html/wp-load.php", validULID, "admin")
			if err == nil {
				t.Errorf("expected error for %s, got nil", c.name)
			}
		})
	}
}

func TestRenderSSOTemplate_RejectsInvalidWpLoadPath(t *testing.T) {
	cases := []struct {
		name, path string
	}{
		{"empty", ""},
		{"relative path", "var/www/html/wp-load.php"},
		{"missing wp-load.php suffix", "/var/www/html/index.php"},
		{"contains semicolon", "/var/www/html;.php/wp-load.php"},
		{"contains single quote", "/var/www/html'/wp-load.php"},
		{"contains dotdot", "/var/www/../etc/wp-load.php"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := RenderSSOTemplate(validNonce, c.path, validULID, "admin")
			if err == nil {
				t.Errorf("expected error for %s, got nil", c.name)
			}
		})
	}
}

func TestRenderSSOTemplate_RejectsInvalidInstallID(t *testing.T) {
	cases := []struct {
		name, id string
	}{
		{"empty", ""},
		{"25 chars", "01ARZ3NDEKTSV4RRFFQ69G5FA"},
		{"27 chars", "01ARZ3NDEKTSV4RRFFQ69G5FAVA"},
		{"lowercase", "01arz3ndektsv4rrffq69g5fav"},
		{"contains I", strings.Repeat("0", 25) + "I"},
		{"contains L", strings.Repeat("0", 25) + "L"},
		{"contains O", strings.Repeat("0", 25) + "O"},
		{"contains U", strings.Repeat("0", 25) + "U"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := RenderSSOTemplate(validNonce, "/var/www/html/wp-load.php", c.id, "admin")
			if err == nil {
				t.Errorf("expected error for %s, got nil", c.name)
			}
		})
	}
}

func TestRenderSSOTemplate_RejectsInvalidAdminUsername(t *testing.T) {
	cases := []struct {
		name, username string
	}{
		{"empty", ""},
		{"too long (61 chars)", strings.Repeat("a", 61)},
		{"contains single quote", "ad'min"},
		{"contains backslash", `ad\min`},
		{"contains semicolon", "ad;min"},
		{"contains slash", "ad/min"},
		{"contains newline", "ad\nmin"},
		{"leading space", " admin"},
		{"trailing space", "admin "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := RenderSSOTemplate(validNonce, "/var/www/html/wp-load.php", validULID, c.username)
			if err == nil {
				t.Errorf("expected error for %s, got nil", c.name)
			}
		})
	}
}

func TestRenderSSOTemplate_AcceptsRealisticUsernames(t *testing.T) {
	cases := []string{
		"admin",
		"a",                 // single char
		"shuki.vaknin",      // dot
		"alice_bob",         // underscore
		"user-1",            // dash
		"someone@example.com",
		"user with space",   // internal space
		strings.Repeat("a", 60), // max length
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			_, err := RenderSSOTemplate(validNonce, "/var/www/html/wp-load.php", validULID, u)
			if err != nil {
				t.Errorf("expected ok for %q, got %v", u, err)
			}
		})
	}
}

func TestRenderSSOTemplate_PassesPHPLint(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not on PATH, skipping syntax check")
	}
	out, err := RenderSSOTemplate(validNonce, "/var/www/html/wp-load.php", validULID, "admin")
	if err != nil {
		t.Fatalf("RenderSSOTemplate: %v", err)
	}
	cmd := exec.Command("php", "-l")
	cmd.Stdin = strings.NewReader(out)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("php -l failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "No syntax errors") {
		t.Errorf("php -l output unexpected: %s", output)
	}
}

func TestRenderSSOTemplate_NoUnsubstitutedMarkers(t *testing.T) {
	out, err := RenderSSOTemplate(validNonce, "/var/www/html/wp-load.php", validULID, "admin")
	if err != nil {
		t.Fatalf("RenderSSOTemplate: %v", err)
	}
	if regexp.MustCompile(`__JABALI_[A-Z_]+__`).MatchString(out) {
		t.Errorf("output still contains a __MARKER__ token")
	}
}

// excerpt returns up to 80 chars around the first occurrence of needle in
// s, for diagnostic test failure messages. Returns "<not found>" if the
// needle is absent.
func excerpt(s, needle string) string {
	i := strings.Index(s, needle)
	if i < 0 {
		return "<not found>"
	}
	start := i - 20
	if start < 0 {
		start = 0
	}
	end := i + 60
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
}
