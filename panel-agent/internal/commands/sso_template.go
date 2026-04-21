// Package commands: SSO file template for M22 rework (ADR-0040).
//
// The PHP template is //go:embed-ed at build time. RenderSSOTemplate
// substitutes per-install placeholders before the agent writes the file
// to the WordPress webroot. The file is self-contained — no plugin, no
// callback, no signing key.

package commands

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

//go:embed sso_template.php
var ssoTemplate string

// ssoTTLSeconds is the inline TTL the rendered file enforces. The systemd
// reaper sweeps every 30s; worst-case stranded-file lifetime is 60+30=90s.
const ssoTTLSeconds = 60

var (
	nonceRE        = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	wpLoadPathRE   = regexp.MustCompile(`^/[A-Za-z0-9_/.\-]+/wp-load\.php$`)
	wpLoadDotDotRE = regexp.MustCompile(`(^|/)\.\.(/|$)`)
	// leftoverMarker matches our namespaced placeholders only — NOT PHP's
	// magic constants like __FILE__ / __LINE__ / __DIR__ which the template
	// uses legitimately.
	leftoverMarker = regexp.MustCompile(`__JABALI_[A-Z_]+__`)
)

// crockfordULID is the Crockford base32 alphabet ULIDs use: 0-9 + A-Z
// minus I, L, O, U. Copied from wordpress_magic_link.go which Step 5
// of the M22 rework will delete.
const crockfordULID = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// GenerateNonce returns 32 bytes of crypto/rand encoded as base64url
// no-padding (43 chars). 256 bits of entropy = brute force is
// computationally infeasible (ADR-0040 T4).
func GenerateNonce() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// RenderSSOTemplate substitutes placeholders in the embedded PHP template.
// All inputs are validated; on any failure returns an empty string and a
// clear error.
//
// We use strings.ReplaceAll (NOT text/template) because `{{` would collide
// with PHP echo blocks if the template ever grows them.
func RenderSSOTemplate(nonce, wpLoadPath, installID string, adminUID int) (string, error) {
	if !nonceRE.MatchString(nonce) {
		return "", fmt.Errorf("invalid nonce: must be 43 base64url chars, got %q", nonce)
	}
	if wpLoadDotDotRE.MatchString(wpLoadPath) {
		return "", fmt.Errorf("invalid wpLoadPath: contains \"..\" component, got %q", wpLoadPath)
	}
	if !wpLoadPathRE.MatchString(wpLoadPath) {
		return "", fmt.Errorf("invalid wpLoadPath: must be absolute, end in wp-load.php, contain only [A-Za-z0-9_/.\\-], got %q", wpLoadPath)
	}
	if !sedSafeULIDLocal(installID) {
		return "", fmt.Errorf("invalid installID: must be 26-char Crockford ULID, got %q", installID)
	}
	if adminUID <= 0 || adminUID >= (1<<31) {
		return "", fmt.Errorf("invalid adminUID: must be > 0 and < 2^31, got %d", adminUID)
	}

	out := ssoTemplate
	out = strings.ReplaceAll(out, "__JABALI_TTL_SECONDS__", fmt.Sprintf("%d", ssoTTLSeconds))
	out = strings.ReplaceAll(out, "__JABALI_WP_LOAD_PATH__", phpStringLiteral(wpLoadPath))
	out = strings.ReplaceAll(out, "__JABALI_ADMIN_UID__", fmt.Sprintf("%d", adminUID))
	// installID and nonce appear in comment + error_log strings only;
	// validators above already restrict their character sets.
	out = strings.ReplaceAll(out, "__JABALI_INSTALL_ID__", installID)
	out = strings.ReplaceAll(out, "__JABALI_NONCE__", nonce)

	if leftoverMarker.MatchString(out) {
		return "", fmt.Errorf("unsubstituted placeholder remains in rendered template")
	}
	return out, nil
}

// phpStringLiteral wraps a value in single quotes, escaping `\` and `'`.
// The wpLoadPath validator already restricts the input to safe chars so
// the escape is paranoia, but defence in depth is cheap.
func phpStringLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// sedSafeULIDLocal validates a 26-char Crockford ULID. Local copy of the
// validator currently in wordpress_magic_link.go (which Step 5 will
// delete). Renamed with the Local suffix so we don't conflict with the
// existing exported name during the migration window.
func sedSafeULIDLocal(s string) bool {
	if len(s) != 26 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(crockfordULID, r) {
			return false
		}
	}
	return true
}
