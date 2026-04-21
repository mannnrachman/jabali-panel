package magiclink

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"
)

var (
	// ErrMalformed indicates the token string is not the expected
	// `<base64url(token_id)>.<base64url(hmac)>` shape: missing the
	// dot separator, non-base64url alphabet, wrong decoded byte
	// counts, etc. Distinguished from ErrSignatureMismatch so the
	// caller can decide whether a malformed token is "user typo"
	// (silent no-op) or "tampering" (audit log).
	ErrMalformed = errors.New("magic link token is malformed")
	// ErrSignatureMismatch indicates the HMAC over
	// (token_id || install_id || expires_at) does not match any key
	// in the keyring. Returned both for forged tokens and for tokens
	// signed under a key the keyring no longer holds (post-rotation).
	ErrSignatureMismatch = errors.New("magic link token signature mismatch")
)

// Verify checks a magic link token against a keyring.
//
// The token format is `<base64url(token_id)>.<base64url(hmac_sig)>`,
// where hmac_sig is HMAC-SHA256 over the binary concatenation
// `(token_id || install_id || expires_at_unix_seconds_LE)`.
//
// Verify is a pure cryptographic check — it does NOT consult the
// database, does NOT check used_at, and does NOT enforce TTL beyond
// the signed expires_at value the caller passes in. The caller is
// expected to: (1) split the token, (2) look up the row by
// SHA256(token_id) to discover the canonical install_id and
// expires_at, (3) call Verify with those values, (4) on success
// proceed to MarkUsed + clock-skew check (model.IsExpiredAt). This
// separation keeps the crypto unit-testable in isolation.
//
// On any error, Verify returns the zero [16]byte for tokenID — never
// the value extracted from an unverified token, which would let a
// caller accidentally trust attacker-controlled bytes.
func Verify(keys *Keys, tokenString string, installID string, expiresAt time.Time) ([16]byte, error) {
	var zero [16]byte

	parts := strings.Split(tokenString, ".")
	if len(parts) != 2 {
		return zero, ErrMalformed
	}

	tokenIDBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(tokenIDBytes) != 16 {
		return zero, ErrMalformed
	}

	providedSig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(providedSig) != sha256.Size {
		return zero, ErrMalformed
	}

	var tokenID [16]byte
	copy(tokenID[:], tokenIDBytes)

	for _, key := range keys.All() {
		expected := computeSignature(key, tokenID, installID, expiresAt)
		if subtle.ConstantTimeCompare(providedSig, expected) == 1 {
			return tokenID, nil
		}
	}

	return zero, ErrSignatureMismatch
}

// computeSignature is the canonical HMAC computation shared by
// signer.go's Sign and verifier.go's Verify. Defined here so the
// two code paths cannot drift; if you change the signed payload
// shape, change this and both call sites in lockstep.
func computeSignature(key Key, tokenID [16]byte, installID string, expiresAt time.Time) []byte {
	h := hmac.New(sha256.New, key[:])
	h.Write(tokenID[:])
	h.Write([]byte(installID))

	var expBytes [8]byte
	binary.LittleEndian.PutUint64(expBytes[:], uint64(expiresAt.Unix()))
	h.Write(expBytes[:])

	return h.Sum(nil)
}
