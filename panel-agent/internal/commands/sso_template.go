// Package commands: SSO file template shared helpers for M22 rework
// (ADR-0040).
//
// Per-CMS PHP templates are //go:embed-ed in each CMS's
// <cms>_create_sso_file.go file. This file exposes only the helpers
// those renderers share: nonce generation, ULID validation, PHP string
// escaping, leftover-placeholder detection, and the inline TTL.

package commands

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

// ssoTTLSeconds is the inline TTL every per-CMS template enforces. The
// systemd reaper sweeps every 30s; worst-case stranded-file lifetime
// is 60+30=90s.
const ssoTTLSeconds = 60

// crockfordULID is the Crockford base32 alphabet ULIDs use: 0-9 + A-Z
// minus I, L, O, U.
const crockfordULID = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	// nonceRE matches the 43-char base64url no-padding nonce.
	nonceRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	// leftoverMarker matches our namespaced placeholders only — NOT PHP's
	// magic constants like __FILE__ / __LINE__ / __DIR__ which the
	// templates use legitimately.
	leftoverMarker = regexp.MustCompile(`__JABALI_[A-Z_]+__`)
)

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

// phpStringLiteral wraps a value in single quotes, escaping `\` and `'`.
// Per-CMS renderers validate their inputs first, so the escape is
// defence in depth against a validator regression.
func phpStringLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// sedSafeULID validates a 26-char Crockford ULID.
func sedSafeULID(s string) bool {
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
