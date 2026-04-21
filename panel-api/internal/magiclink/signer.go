package magiclink

import (
	"encoding/base64"
	"io"
	"time"
)

// Generate returns a fresh random 16-byte token ID sourced from rng.
// Callers pass crypto/rand.Reader in production and a deterministic
// source in tests.
func Generate(rng io.Reader) ([16]byte, error) {
	var tokenID [16]byte
	_, err := io.ReadFull(rng, tokenID[:])
	return tokenID, err
}

// Sign returns the wire form of a magic link token:
//
//	<base64url(tokenID)>.<base64url(hmac_sig)>
//
// where hmac_sig is HMAC-SHA256(key, tokenID || installID ||
// expiresAt_unix_seconds_LE). Little-endian because it's the panel's
// host byte order on every platform we build for; the choice doesn't
// matter for security, only for the signer/verifier to agree.
//
// The actual HMAC computation lives in computeSignature (verifier.go)
// so Sign and Verify cannot drift.
func Sign(key Key, tokenID [16]byte, installID string, expiresAt time.Time) string {
	sig := computeSignature(key, tokenID, installID, expiresAt)
	return base64.RawURLEncoding.EncodeToString(tokenID[:]) +
		"." +
		base64.RawURLEncoding.EncodeToString(sig)
}
