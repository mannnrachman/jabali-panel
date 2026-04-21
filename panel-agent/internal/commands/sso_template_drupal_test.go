package commands

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestRenderDrupalSSOTemplate_HappyPath(t *testing.T) {
	out, err := RenderDrupalSSOTemplate(validNonce, "/var/www/html/2/autoload.php", validULID, "kiaepj")
	if err != nil {
		t.Fatalf("RenderDrupalSSOTemplate: %v", err)
	}
	if !strings.Contains(out, validNonce) {
		t.Errorf("output missing nonce")
	}
	if !strings.Contains(out, validULID) {
		t.Errorf("output missing install id")
	}
	if !strings.Contains(out, "$admin_username = 'kiaepj';") {
		t.Errorf("output missing admin_username literal")
	}
	if !strings.Contains(out, "'/var/www/html/2/autoload.php'") {
		t.Errorf("output missing single-quoted autoload.php path")
	}
	if !strings.Contains(out, "60 < time()") {
		t.Errorf("output missing TTL_SECONDS substitution")
	}
	if regexp.MustCompile(`__JABALI_[A-Z_]+__`).MatchString(out) {
		t.Errorf("output still contains a __MARKER__ token")
	}
}

func TestRenderDrupalSSOTemplate_RejectsInvalidAutoload(t *testing.T) {
	cases := []struct {
		name, path string
	}{
		{"empty", ""},
		{"relative", "autoload.php"},
		{"dotdot", "/var/www/../autoload.php"},
		{"wrong-suffix", "/var/www/wp-load.php"},
		{"non-ascii", "/var/www/\x00autoload.php"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RenderDrupalSSOTemplate(validNonce, tc.path, validULID, "admin"); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestRenderDrupalSSOTemplate_GuardsAllSpeculativeFetches(t *testing.T) {
	out, err := RenderDrupalSSOTemplate(validNonce, "/var/www/html/autoload.php", validULID, "admin")
	if err != nil {
		t.Fatalf("RenderDrupalSSOTemplate: %v", err)
	}
	if !strings.Contains(out, "$secPurpose !== ''") {
		t.Errorf("template missing forward-compatible Sec-Purpose guard")
	}
}

func TestRenderDrupalSSOTemplate_PassesPHPLint(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not on PATH, skipping syntax check")
	}
	out, err := RenderDrupalSSOTemplate(validNonce, "/var/www/html/autoload.php", validULID, "admin")
	if err != nil {
		t.Fatalf("RenderDrupalSSOTemplate: %v", err)
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
