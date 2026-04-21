package commands

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestRenderJoomlaSSOTemplate_HappyPath(t *testing.T) {
	out, err := RenderJoomlaSSOTemplate(validNonce, "/home/u/domains/example.com/public_html/joomla", validULID, "kiaepj")
	if err != nil {
		t.Fatalf("RenderJoomlaSSOTemplate: %v", err)
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
	if !strings.Contains(out, "'/home/u/domains/example.com/public_html/joomla'") {
		t.Errorf("output missing single-quoted install path")
	}
	if !strings.Contains(out, "60 < time()") {
		t.Errorf("output missing TTL_SECONDS substitution")
	}
	if regexp.MustCompile(`__JABALI_[A-Z_]+__`).MatchString(out) {
		t.Errorf("output still contains a __MARKER__ token")
	}
}

func TestRenderJoomlaSSOTemplate_RejectsInvalidInstallPath(t *testing.T) {
	cases := []struct {
		name, path string
	}{
		{"empty", ""},
		{"relative", "joomla"},
		{"dotdot", "/var/www/../joomla"},
		{"non-ascii", "/var/www/\x00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RenderJoomlaSSOTemplate(validNonce, tc.path, validULID, "admin"); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestRenderJoomlaSSOTemplate_GuardsAllSpeculativeFetches(t *testing.T) {
	out, err := RenderJoomlaSSOTemplate(validNonce, "/var/www/html", validULID, "admin")
	if err != nil {
		t.Fatalf("RenderJoomlaSSOTemplate: %v", err)
	}
	if !strings.Contains(out, "$secPurpose !== ''") {
		t.Errorf("template missing forward-compatible Sec-Purpose guard")
	}
}

func TestRenderJoomlaSSOTemplate_PassesPHPLint(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not on PATH, skipping syntax check")
	}
	out, err := RenderJoomlaSSOTemplate(validNonce, "/var/www/html", validULID, "admin")
	if err != nil {
		t.Fatalf("RenderJoomlaSSOTemplate: %v", err)
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
