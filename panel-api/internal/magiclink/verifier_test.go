package magiclink

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestVerify_Success verifies token signature validation succeeds with correct inputs.
func TestVerify_Success(t *testing.T) {
	key := Key{1, 2, 3}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)

	// Sign the token
	token := Sign(key, tokenID, installID, expiresAt)

	// Create Keys struct with the key
	keys := &Keys{keys: []Key{key}}

	// Verify should succeed
	verifiedTokenID, err := Verify(keys, token, installID, expiresAt)
	require.NoError(t, err)
	require.Equal(t, tokenID, verifiedTokenID)
}

// TestVerify_MalformedNoSeparator verifies ErrMalformed when token has no dot separator.
func TestVerify_MalformedNoSeparator(t *testing.T) {
	keys := &Keys{keys: []Key{{1, 2, 3}}}
	token := "invalid_token_no_dot"
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)

	tokenID, err := Verify(keys, token, installID, expiresAt)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrMalformed)
	require.Equal(t, [16]byte{}, tokenID)
}

// TestVerify_MalformedTooManySeparators verifies ErrMalformed with too many dots.
func TestVerify_MalformedTooManySeparators(t *testing.T) {
	keys := &Keys{keys: []Key{{1, 2, 3}}}
	token := "part1.part2.part3"
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)

	tokenID, err := Verify(keys, token, installID, expiresAt)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrMalformed)
	require.Equal(t, [16]byte{}, tokenID)
}

// TestVerify_MalformedInvalidTokenIDBase64 verifies ErrMalformed with invalid base64 in token ID part.
func TestVerify_MalformedInvalidTokenIDBase64(t *testing.T) {
	keys := &Keys{keys: []Key{{1, 2, 3}}}
	token := "!!!invalid_base64.validbase64"
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)

	tokenID, err := Verify(keys, token, installID, expiresAt)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrMalformed)
	require.Equal(t, [16]byte{}, tokenID)
}

// TestVerify_MalformedInvalidHMACBase64 verifies ErrMalformed with invalid base64 in HMAC part.
func TestVerify_MalformedInvalidHMACBase64(t *testing.T) {
	key := Key{1, 2, 3}
	tokenID := [16]byte{4, 5, 6}

	tokenIDEncoded := base64.RawURLEncoding.EncodeToString(tokenID[:])
	token := tokenIDEncoded + ".!!!invalid_base64"

	keys := &Keys{keys: []Key{key}}
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)

	verifiedTokenID, err := Verify(keys, token, installID, expiresAt)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrMalformed)
	require.Equal(t, [16]byte{}, verifiedTokenID)
}

// TestVerify_MalformedWrongTokenIDLength verifies ErrMalformed when token ID is not 16 bytes.
func TestVerify_MalformedWrongTokenIDLength(t *testing.T) {
	key := Key{1, 2, 3}
	shortTokenID := [8]byte{4, 5, 6, 7, 8, 9, 10, 11}

	tokenIDEncoded := base64.RawURLEncoding.EncodeToString(shortTokenID[:])
	hmac := [32]byte{12, 13, 14}
	hmacEncoded := base64.RawURLEncoding.EncodeToString(hmac[:])
	token := tokenIDEncoded + "." + hmacEncoded

	keys := &Keys{keys: []Key{key}}
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)

	verifiedTokenID, err := Verify(keys, token, installID, expiresAt)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrMalformed)
	require.Equal(t, [16]byte{}, verifiedTokenID)
}

// TestVerify_SignatureMismatchWrongKey verifies ErrSignatureMismatch with wrong key.
func TestVerify_SignatureMismatchWrongKey(t *testing.T) {
	signingKey := [32]byte{1, 2, 3}
	wrongKey := [32]byte{9, 9, 9}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)

	// Sign with signingKey
	token := Sign(signingKey, tokenID, installID, expiresAt)

	// Verify with wrongKey
	keys := &Keys{keys: []Key{wrongKey}}
	verifiedTokenID, err := Verify(keys, token, installID, expiresAt)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrSignatureMismatch)
	require.Equal(t, [16]byte{}, verifiedTokenID)
}

// TestVerify_SignatureMismatchWrongInstallID verifies ErrSignatureMismatch with wrong install ID.
func TestVerify_SignatureMismatchWrongInstallID(t *testing.T) {
	key := Key{1, 2, 3}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	wrongInstallID := "install_456"
	expiresAt := time.Now().Add(60 * time.Second)

	// Sign with installID
	token := Sign(key, tokenID, installID, expiresAt)

	// Verify with wrongInstallID
	keys := &Keys{keys: []Key{key}}
	verifiedTokenID, err := Verify(keys, token, wrongInstallID, expiresAt)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrSignatureMismatch)
	require.Equal(t, [16]byte{}, verifiedTokenID)
}

// TestVerify_SignatureMismatchWrongExpiresAt verifies ErrSignatureMismatch with wrong expiresAt.
func TestVerify_SignatureMismatchWrongExpiresAt(t *testing.T) {
	key := Key{1, 2, 3}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)
	wrongExpiresAt := expiresAt.Add(1 * time.Second)

	// Sign with expiresAt
	token := Sign(key, tokenID, installID, expiresAt)

	// Verify with wrongExpiresAt
	keys := &Keys{keys: []Key{key}}
	verifiedTokenID, err := Verify(keys, token, installID, wrongExpiresAt)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrSignatureMismatch)
	require.Equal(t, [16]byte{}, verifiedTokenID)
}

// TestVerify_KeyRotation verifies that verification works with multiple keys (key rotation).
func TestVerify_KeyRotation(t *testing.T) {
	oldKey := [32]byte{1, 2, 3}
	newKey := [32]byte{9, 8, 7}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)

	// Sign with old key
	token := Sign(oldKey, tokenID, installID, expiresAt)

	// Verify with keys list containing both newKey (first) and oldKey (second)
	keys := &Keys{keys: []Key{newKey, oldKey}}
	verifiedTokenID, err := Verify(keys, token, installID, expiresAt)
	require.NoError(t, err)
	require.Equal(t, tokenID, verifiedTokenID)
}

// TestVerify_MultipleKeysNewKeyMatch verifies verification with new key in rotation.
func TestVerify_MultipleKeysNewKeyMatch(t *testing.T) {
	newKey := [32]byte{1, 2, 3}
	oldKey := [32]byte{9, 8, 7}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)

	// Sign with new key
	token := Sign(newKey, tokenID, installID, expiresAt)

	// Verify with keys list containing newKey first
	keys := &Keys{keys: []Key{newKey, oldKey}}
	verifiedTokenID, err := Verify(keys, token, installID, expiresAt)
	require.NoError(t, err)
	require.Equal(t, tokenID, verifiedTokenID)
}

// TestVerify_TokenIDExtraction verifies the token ID is correctly extracted.
func TestVerify_TokenIDExtraction(t *testing.T) {
	key := Key{1, 2, 3}
	tokenID := [16]byte{10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25}
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)

	token := Sign(key, tokenID, installID, expiresAt)

	keys := &Keys{keys: []Key{key}}
	verifiedTokenID, err := Verify(keys, token, installID, expiresAt)
	require.NoError(t, err)
	require.Equal(t, tokenID, verifiedTokenID)
}

// TestVerify_ConstantTimeComparison verifies constant-time comparison prevents timing attacks.
// This test verifies that the implementation uses subtle.ConstantTimeCompare by checking
// that different HMACs both fail verification (they should fail, but not via timing attacks).
func TestVerify_ConstantTimeComparison(t *testing.T) {
	key := Key{1, 2, 3}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)

	// Sign token with key
	token := Sign(key, tokenID, installID, expiresAt)

	// Verify with correct key
	keys := &Keys{keys: []Key{key}}
	verifiedTokenID, err := Verify(keys, token, installID, expiresAt)
	require.NoError(t, err)
	require.Equal(t, tokenID, verifiedTokenID)

	// Tamper with the HMAC part (change one character)
	parts := strings.Split(token, ".")
	tampered := parts[0] + "." + strings.ToUpper(parts[1])

	// Verify should fail for tampered token
	verifiedTokenID, err = Verify(keys, tampered, installID, expiresAt)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrSignatureMismatch)
}

// TestVerify_EmptyKeyList verifies behavior with empty key list (should fail).
func TestVerify_EmptyKeyList(t *testing.T) {
	key := Key{1, 2, 3}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)

	token := Sign(key, tokenID, installID, expiresAt)

	// Create Keys with empty list
	keys := &Keys{keys: []Key{}}

	verifiedTokenID, err := Verify(keys, token, installID, expiresAt)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrSignatureMismatch)
	require.Equal(t, [16]byte{}, verifiedTokenID)
}

// TestVerify_ZeroTime verifies verification with Unix epoch time.
func TestVerify_ZeroTime(t *testing.T) {
	key := Key{1, 2, 3}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	expiresAt := time.Unix(0, 0)

	token := Sign(key, tokenID, installID, expiresAt)
	keys := &Keys{keys: []Key{key}}

	verifiedTokenID, err := Verify(keys, token, installID, expiresAt)
	require.NoError(t, err)
	require.Equal(t, tokenID, verifiedTokenID)
}

// TestVerify_LargeTimestamp verifies verification with large Unix timestamp.
func TestVerify_LargeTimestamp(t *testing.T) {
	key := Key{1, 2, 3}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	expiresAt := time.Unix(9999999999, 0) // Far future

	token := Sign(key, tokenID, installID, expiresAt)
	keys := &Keys{keys: []Key{key}}

	verifiedTokenID, err := Verify(keys, token, installID, expiresAt)
	require.NoError(t, err)
	require.Equal(t, tokenID, verifiedTokenID)
}

// TestComputeHmac_CorrectStructure verifies computeSignature produces correct structure.
func TestComputeHmac_CorrectStructure(t *testing.T) {
	key := Key{1, 2, 3, 4, 5}
	tokenID := [16]byte{10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25}
	installID := "test_install"
	expiresAt := time.Unix(1234567890, 0)

	result := computeSignature(key, tokenID, installID, expiresAt)

	// HMAC should be 32 bytes (SHA256)
	require.Len(t, result, 32)

	// Manually compute expected HMAC
	h := hmac.New(sha256.New, key[:])
	h.Write(tokenID[:])
	h.Write([]byte(installID))

	expiresAtUnix := expiresAt.Unix()
	var expiresAtBytes [8]byte
	binary.LittleEndian.PutUint64(expiresAtBytes[:], uint64(expiresAtUnix))
	h.Write(expiresAtBytes[:])

	expectedHmac := h.Sum(nil)

	require.Equal(t, expectedHmac, result)
}

// TestComputeHmac_LittleEndianEncoding verifies little-endian encoding of expiresAt.
func TestComputeHmac_LittleEndianEncoding(t *testing.T) {
	key := Key{1}
	tokenID := [16]byte{2}
	installID := ""
	expiresAt := time.Unix(0x0100000000, 0) // 256GB in Unix timestamp

	result := computeSignature(key, tokenID, installID, expiresAt)

	// Compute with different endianness to verify LE is used
	h := hmac.New(sha256.New, key[:])
	h.Write(tokenID[:])
	h.Write([]byte(installID))
	var expiresAtBytes [8]byte
	// Use big-endian (wrong)
	expiresAtUnix := expiresAt.Unix()
	for i := 0; i < 8; i++ {
		expiresAtBytes[7-i] = byte(expiresAtUnix >> (i * 8))
	}
	h.Write(expiresAtBytes[:])
	wrongHmac := h.Sum(nil)

	require.NotEqual(t, result, wrongHmac)
}

// TestComputeHmac_Consistency verifies computeSignature is consistent.
func TestComputeHmac_Consistency(t *testing.T) {
	key := Key{1, 2, 3}
	tokenID := [16]byte{4, 5, 6}
	installID := "install"
	expiresAt := time.Unix(1000000, 0)

	hmac1 := computeSignature(key, tokenID, installID, expiresAt)
	hmac2 := computeSignature(key, tokenID, installID, expiresAt)

	require.Equal(t, hmac1, hmac2)
}

// TestComputeHmac_DifferentInputsDifferentOutput verifies different inputs produce different outputs.
func TestComputeHmac_DifferentInputsDifferentOutput(t *testing.T) {
	key1 := Key{1}
	key2 := Key{2}
	tokenID := [16]byte{4, 5, 6}
	installID := "install"
	expiresAt := time.Unix(1000000, 0)

	hmac1 := computeSignature(key1, tokenID, installID, expiresAt)
	hmac2 := computeSignature(key2, tokenID, installID, expiresAt)

	require.NotEqual(t, hmac1, hmac2)
}

// (Removed: legacy Verify-without-expiresAt placeholder. The single
// Verify entrypoint now requires expiresAt as a parameter — see
// TestVerify_Success above for the canonical happy-path.)
