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
	out, err := RenderSSOTemplate(validNonce, "/var/www/html/wp-load.php", validULID, 1)
	if err != nil {
		t.Fatalf("RenderSSOTemplate: %v", err)
	}
	if !strings.Contains(out, validNonce) {
		t.Errorf("output missing nonce")
	}
	if !strings.Contains(out, validULID) {
		t.Errorf("output missing install id")
	}
	if !strings.Contains(out, "$admin_uid = 1;") {
		t.Errorf("output missing admin_uid integer literal: %s", excerpt(out, "$admin_uid"))
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
			_, err := RenderSSOTemplate(c.nonce, "/var/www/html/wp-load.php", validULID, 1)
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
			_, err := RenderSSOTemplate(validNonce, c.path, validULID, 1)
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
			_, err := RenderSSOTemplate(validNonce, "/var/www/html/wp-load.php", c.id, 1)
			if err == nil {
				t.Errorf("expected error for %s, got nil", c.name)
			}
		})
	}
}

func TestRenderSSOTemplate_RejectsInvalidAdminUID(t *testing.T) {
	cases := []struct {
		name string
		uid  int
	}{
		{"zero", 0},
		{"negative", -1},
		{"2^31", 1 << 31},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := RenderSSOTemplate(validNonce, "/var/www/html/wp-load.php", validULID, c.uid)
			if err == nil {
				t.Errorf("expected error for %s, got nil", c.name)
			}
		})
	}
}

func TestRenderSSOTemplate_PassesPHPLint(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not on PATH, skipping syntax check")
	}
	out, err := RenderSSOTemplate(validNonce, "/var/www/html/wp-load.php", validULID, 1)
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
	out, err := RenderSSOTemplate(validNonce, "/var/www/html/wp-load.php", validULID, 1)
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
