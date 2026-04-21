package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// muPluginSourcePath is the install-time location of the canonical
// jabali-magic-link.php (shipped by install.sh's
// install_jabali_wp_mu_plugin step). Per-install copies are made from
// this path on every WP install so a single `jabali update` propagates
// fixes to all future installs without touching existing ones.
const muPluginSourcePath = "/usr/local/lib/jabali/wp-mu-plugins/jabali-magic-link.php"

// installMagicLinkMUPlugin copies the canonical jabali-magic-link.php
// into <installPath>/wp-content/mu-plugins/, sed-substitutes the panel
// host + install id constants, then sets <user>:www-data 0640.
//
// Idempotent: a subsequent install replaces the file in place. Safe to
// call from a re-run of wordpress.install (e.g., after a transient
// network failure during wp core install).
func installMagicLinkMUPlugin(ctx context.Context, installPath, osUser, panelHost, installID string) error {
	if _, err := os.Stat(muPluginSourcePath); err != nil {
		// Missing source means the host hasn't been re-installed since
		// M22 shipped — surface a clear error rather than silently
		// skipping the mu-plugin install.
		return fmt.Errorf("mu-plugin source missing at %s (run install.sh to refresh): %w", muPluginSourcePath, err)
	}

	muDir := filepath.Join(installPath, "wp-content", "mu-plugins")
	if err := exec.CommandContext(ctx, "mkdir", "-p", muDir).Run(); err != nil {
		return fmt.Errorf("mkdir %s: %w", muDir, err)
	}

	dest := filepath.Join(muDir, "jabali-magic-link.php")
	if err := exec.CommandContext(ctx, "cp", "-f", muPluginSourcePath, dest).Run(); err != nil {
		return fmt.Errorf("cp %s -> %s: %w", muPluginSourcePath, dest, err)
	}

	// Sanity-check the inputs before they reach sed: a single quote or
	// slash in either substitution would break the sed expression. The
	// panel-side caller already constrains these — host is a hostname
	// (RFC 1123) and install id is a ULID — but defence-in-depth is
	// cheap here and the failure message is more useful than a sed
	// error mid-pipeline.
	if !sedSafeHost(panelHost) {
		return fmt.Errorf("unsafe panel host %q", panelHost)
	}
	if !sedSafeULID(installID) {
		return fmt.Errorf("unsafe install id %q", installID)
	}

	subs := []struct {
		placeholder string
		value       string
	}{
		{"__PANEL_HOST__", panelHost},
		{"__INSTALL_ID__", installID},
	}
	for _, sub := range subs {
		expr := fmt.Sprintf("s|%s|%s|g", sub.placeholder, sub.value)
		if err := exec.CommandContext(ctx, "sed", "-i", expr, dest).Run(); err != nil {
			return fmt.Errorf("sed %q in %s: %w", expr, dest, err)
		}
	}

	if err := exec.CommandContext(ctx, "chown", osUser+":www-data", dest).Run(); err != nil {
		return fmt.Errorf("chown %s:www-data %s: %w", osUser, dest, err)
	}
	if err := exec.CommandContext(ctx, "chmod", "0640", dest).Run(); err != nil {
		return fmt.Errorf("chmod 0640 %s: %w", dest, err)
	}
	return nil
}

// sedSafeHost rejects any character that would break the `s|...|...|g`
// expression or escape into shell. Hostnames are letters, digits, dots,
// and hyphens; reject everything else.
func sedSafeHost(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '-':
		default:
			return false
		}
	}
	return true
}

// sedSafeULID accepts the 26-char Crockford base32 alphabet ULIDs use:
// 0-9 + A-Z minus I, L, O, U. Defensive — the caller already constrains
// this via panel-api/internal/ids.
func sedSafeULID(s string) bool {
	if len(s) != 26 {
		return false
	}
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	for _, r := range s {
		if !strings.ContainsRune(alphabet, r) {
			return false
		}
	}
	return true
}
