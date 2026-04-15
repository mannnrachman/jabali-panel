package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// GenerateRefreshToken returns (raw, hash) where raw is a 256-bit random
// token in hex (64 chars) and hash is SHA-256(raw) in hex. Only `hash` is
// stored in the DB; `raw` goes to the client in an HttpOnly cookie and is
// never written to logs or persistent storage on our side.
func GenerateRefreshToken() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("auth: read random bytes: %w", err)
	}
	raw = hex.EncodeToString(buf)
	hash = HashRefreshToken(raw)
	return raw, hash, nil
}

// HashRefreshToken returns the storage-safe hash of a raw refresh token.
// Exposed so Logout / lookup callers can turn an incoming cookie value into
// the DB key without duplicating the hashing code.
func HashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// DeriveDeviceID returns a stable identifier for the client device. If the
// client passes an explicit X-Device-Id header (e.g. a UUID the SPA mints
// on first load and stores in localStorage), use that verbatim. Otherwise
// fall back to SHA-256 of the User-Agent + IP so revocation is still
// per-device-ish.
//
// Caveat: the fallback changes if the client's UA version bumps or their IP
// changes behind CGNAT. For robust per-device revocation, get the SPA to
// send an explicit header. Blueprint note for Phase 8.
func DeriveDeviceID(header, userAgent, ip string) string {
	if header != "" {
		return header
	}
	sum := sha256.Sum256([]byte(userAgent + "|" + ip))
	return hex.EncodeToString(sum[:])
}
