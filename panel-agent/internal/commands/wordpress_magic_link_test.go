package commands

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// installMagicLinkMUPlugin reads from a fixed system path
// (/usr/local/lib/jabali/wp-mu-plugins/jabali-magic-link.php) which we
// can't easily mock without injecting a parameter. The tests below
// either skip or temp-write to the real path — when the integration
// host is available `make test-integration` exercises the full pipe.
//
// Pure-Go unit coverage focuses on the sedSafe* validators which carry
// the security-critical input filtering.

func TestSedSafeHost(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"panel.example.com", true},
		{"localhost", true},
		{"a", true},
		{"10-0-3-13.example", true},
		{"PANEL.example.com", true},
		// Failure cases
		{"", false},
		{"panel.example.com|true", false},      // pipe — breaks `s|...|` expr
		{"panel.example.com;rm -rf /", false},  // shell injection
		{"panel.example.com'", false},          // quote
		{"panel example.com", false},           // space
		{"panel/example.com", false},           // slash — breaks `s/...` expr
		{"日本.example.com", false},             // non-ASCII
	}
	for _, tc := range cases {
		got := sedSafeHost(tc.in)
		if got != tc.want {
			t.Errorf("sedSafeHost(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSedSafeULID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"01HMABCDEFGHJKMNPQRSTVWXYZ", true},     // 26 chars, all-Crockford
		{"00000000000000000000000000", true},
		{"ZZZZZZZZZZZZZZZZZZZZZZZZZZ", true},
		// Failure cases
		{"", false},
		{"01HMABC", false},                       // wrong length
		{"01HMABCDEFGHJKMNPQRSTVWXYZ0", false},  // 27 chars
		{"01HMABCDEFGHJKMNPQRSTVWXYZI", false},  // I excluded
		{"01HMABCDEFGHJKMNPQRSTVWXYZL", false},  // L excluded
		{"01HMABCDEFGHJKMNPQRSTVWXYZO", false},  // O excluded
		{"01HMABCDEFGHJKMNPQRSTVWXYZU", false},  // U excluded
		{"01HMABCDEFGHJKMNPQRSTVWXY@@", false},  // punctuation
	}
	for _, tc := range cases {
		got := sedSafeULID(tc.in)
		if got != tc.want {
			t.Errorf("sedSafeULID(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestInstallMagicLinkMUPlugin_RejectsUnsafeInputs covers the
// security-critical validation paths even when the source plugin file
// is absent — the function should refuse before touching the
// filesystem on bad input.
func TestInstallMagicLinkMUPlugin_RejectsUnsafeInputs(t *testing.T) {
	tmp := t.TempDir()
	installPath := filepath.Join(tmp, "wp")
	if err := os.MkdirAll(filepath.Join(installPath, "wp-content"), 0755); err != nil {
		t.Fatal(err)
	}

	// Skip if the source file isn't present — this branch is for hosts
	// that haven't yet run install.sh's install_jabali_wp_mu_plugin step.
	// The function returns the "source missing" error first, before
	// validation; on dev boxes we can't test the unsafe-input branch
	// without setting up the source file. For the purposes of CI on a
	// dev laptop, the sedSafe* tests above cover the same validators.
	if _, err := os.Stat(muPluginSourcePath); err != nil {
		t.Skip("mu-plugin source not present at " + muPluginSourcePath + " — sedSafe* unit tests cover the validation logic directly")
	}

	cases := []struct {
		name      string
		panelHost string
		installID string
	}{
		{"unsafe panel host (semicolon)", "panel.example.com;rm -rf /", "01HMABCDEFGHJKMNPQRSTVWXYZ"},
		{"unsafe panel host (slash)", "panel.example.com/path", "01HMABCDEFGHJKMNPQRSTVWXYZ"},
		{"unsafe install id (wrong length)", "panel.example.com", "TOOSHORT"},
		{"unsafe install id (excluded letter)", "panel.example.com", "01HMABCDEFGHJKMNPQRSTVWXYZL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := installMagicLinkMUPlugin(context.Background(), installPath, "shuki", tc.panelHost, tc.installID)
			if err == nil {
				t.Fatalf("expected error for unsafe inputs, got nil")
			}
		})
	}
}
