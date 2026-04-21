package magiclink

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestGenerate_Success verifies that Generate produces 16 random bytes.
func TestGenerate_Success(t *testing.T) {
	tokenID, err := Generate(rand.Reader)
	require.NoError(t, err)
	require.Len(t, tokenID, 16)
}

// TestGenerate_Uniqueness verifies that Generate produces unique values on repeated calls.
func TestGenerate_Uniqueness(t *testing.T) {
	tokenID1, err := Generate(rand.Reader)
	require.NoError(t, err)

	tokenID2, err := Generate(rand.Reader)
	require.NoError(t, err)

	require.NotEqual(t, tokenID1, tokenID2)
}

// TestGenerate_Multiple verifies multiple tokens are unique.
func TestGenerate_Multiple(t *testing.T) {
	tokenIDs := make(map[[16]byte]bool)
	for i := 0; i < 100; i++ {
		tokenID, err := Generate(rand.Reader)
		require.NoError(t, err)

		require.False(t, tokenIDs[tokenID], "duplicate token ID generated")
		tokenIDs[tokenID] = true
	}

	require.Len(t, tokenIDs, 100)
}

// TestSign_FormatValid verifies that Sign produces the correct token format.
func TestSign_FormatValid(t *testing.T) {
	key := Key{1, 2, 3}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	expiresAt := time.Now().Add(60 * time.Second)

	token := Sign(key, tokenID, installID, expiresAt)

	// Token should be: base64url(tokenID) + "." + base64url(hmac)
	parts := strings.Split(token, ".")
	require.Len(t, parts, 2)

	// Decode and verify token ID part
	decodedTokenID, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	require.Equal(t, tokenID[:], decodedTokenID)

	// Verify HMAC part is valid base64url
	decodedHmac, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	require.Len(t, decodedHmac, 32) // SHA256 produces 32 bytes
}

// TestSign_HMACConsistency verifies that Sign produces consistent HMAC for same inputs.
func TestSign_HMACConsistency(t *testing.T) {
	key := Key{1, 2, 3}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	expiresAt := time.Unix(1000000, 0)

	token1 := Sign(key, tokenID, installID, expiresAt)
	token2 := Sign(key, tokenID, installID, expiresAt)

	require.Equal(t, token1, token2, "sign should produce consistent output")
}

// TestSign_DifferentKeysDifferentHMAC verifies that different keys produce different HMACs.
func TestSign_DifferentKeysDifferentHMAC(t *testing.T) {
	key1 := Key{1}
	key2 := Key{2}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	expiresAt := time.Unix(1000000, 0)

	token1 := Sign(key1, tokenID, installID, expiresAt)
	token2 := Sign(key2, tokenID, installID, expiresAt)

	require.NotEqual(t, token1, token2)
}

// TestSign_DifferentTokenIDsDifferentHMAC verifies that different token IDs produce different HMACs.
func TestSign_DifferentTokenIDsDifferentHMAC(t *testing.T) {
	key := Key{1}
	tokenID1 := [16]byte{4}
	tokenID2 := [16]byte{5}
	installID := "install_123"
	expiresAt := time.Unix(1000000, 0)

	token1 := Sign(key, tokenID1, installID, expiresAt)
	token2 := Sign(key, tokenID2, installID, expiresAt)

	require.NotEqual(t, token1, token2)
}

// TestSign_DifferentInstallIDsDifferentHMAC verifies that different install IDs produce different HMACs.
func TestSign_DifferentInstallIDsDifferentHMAC(t *testing.T) {
	key := Key{1}
	tokenID := [16]byte{4, 5, 6}
	installID1 := "install_123"
	installID2 := "install_456"
	expiresAt := time.Unix(1000000, 0)

	token1 := Sign(key, tokenID, installID1, expiresAt)
	token2 := Sign(key, tokenID, installID2, expiresAt)

	require.NotEqual(t, token1, token2)
}

// TestSign_DifferentExpiresAtDifferentHMAC verifies that different expiration times produce different HMACs.
func TestSign_DifferentExpiresAtDifferentHMAC(t *testing.T) {
	key := Key{1}
	tokenID := [16]byte{4, 5, 6}
	installID := "install_123"
	expiresAt1 := time.Unix(1000000, 0)
	expiresAt2 := time.Unix(1000001, 0)

	token1 := Sign(key, tokenID, installID, expiresAt1)
	token2 := Sign(key, tokenID, installID, expiresAt2)

	require.NotEqual(t, token1, token2)
}

// TestSign_HMACStructure verifies the HMAC is computed correctly over tokenID || installID || expiresAt_unix_le.
func TestSign_HMACStructure(t *testing.T) {
	key := Key{1, 2, 3, 4, 5}
	tokenID := [16]byte{10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25}
	installID := "test_install"
	expiresAt := time.Unix(1234567890, 0)

	token := Sign(key, tokenID, installID, expiresAt)
	parts := strings.Split(token, ".")
	require.Len(t, parts, 2)

	decodedHmac, _ := base64.RawURLEncoding.DecodeString(parts[1])

	// Manually compute expected HMAC
	h := hmac.New(sha256.New, key[:])
	h.Write(tokenID[:])
	h.Write([]byte(installID))

	expiresAtUnix := expiresAt.Unix()
	var expiresAtBytes [8]byte
	binary.LittleEndian.PutUint64(expiresAtBytes[:], uint64(expiresAtUnix))
	h.Write(expiresAtBytes[:])

	expectedHmac := h.Sum(nil)

	require.Equal(t, expectedHmac, decodedHmac)
}

// TestSign_LittleEndianExpiresAt verifies that expiresAt is encoded as little-endian.
func TestSign_LittleEndianExpiresAt(t *testing.T) {
	key := Key{1}
	tokenID := [16]byte{2}
	installID := ""
	expiresAt := time.Unix(0x0100000000, 0) // Should encode as 00 00 00 01 00 00 00 00 in LE

	token1 := Sign(key, tokenID, installID, expiresAt)

	// Use a different endianness to verify LE is used
	h := hmac.New(sha256.New, key[:])
	h.Write(tokenID[:])
	h.Write([]byte(installID))
	var expiresAtBytes [8]byte
	// Intentionally use big-endian to show it's different
	expiresAtUnix := expiresAt.Unix()
	for i := 0; i < 8; i++ {
		expiresAtBytes[7-i] = byte(expiresAtUnix >> (i * 8))
	}
	h.Write(expiresAtBytes[:])
	wrongHmac := h.Sum(nil)
	wrongToken := base64.RawURLEncoding.EncodeToString(tokenID[:]) + "." + base64.RawURLEncoding.EncodeToString(wrongHmac)

	require.NotEqual(t, token1, wrongToken)
}

// TestSign_ZeroExpiresAt verifies signing with zero Unix time.
func TestSign_ZeroExpiresAt(t *testing.T) {
	key := Key{1}
	tokenID := [16]byte{2}
	installID := "test"
	expiresAt := time.Unix(0, 0)

	token := Sign(key, tokenID, installID, expiresAt)
	require.NotEmpty(t, token)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 2)
}

// TestSign_NegativeExpiresAt verifies signing with negative Unix time (before epoch).
func TestSign_NegativeExpiresAt(t *testing.T) {
	key := Key{1}
	tokenID := [16]byte{2}
	installID := "test"
	expiresAt := time.Unix(-1000, 0) // Before Unix epoch

	token := Sign(key, tokenID, installID, expiresAt)
	require.NotEmpty(t, token)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 2)
}

// TestSign_EmptyInstallID verifies signing with empty install ID.
func TestSign_EmptyInstallID(t *testing.T) {
	key := Key{1}
	tokenID := [16]byte{2}
	installID := ""
	expiresAt := time.Unix(1000000, 0)

	token := Sign(key, tokenID, installID, expiresAt)
	require.NotEmpty(t, token)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 2)
}

// TestSign_LongInstallID verifies signing with a long install ID.
func TestSign_LongInstallID(t *testing.T) {
	key := Key{1}
	tokenID := [16]byte{2}
	installID := "install_" + strings.Repeat("x", 1000)
	expiresAt := time.Unix(1000000, 0)

	token := Sign(key, tokenID, installID, expiresAt)
	require.NotEmpty(t, token)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 2)
}

// TestSign_TokenIDPart verifies the token ID part of the signature.
func TestSign_TokenIDPart(t *testing.T) {
	key := Key{1}
	tokenID := [16]byte{10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25}
	installID := "test"
	expiresAt := time.Unix(1000000, 0)

	token := Sign(key, tokenID, installID, expiresAt)
	parts := strings.Split(token, ".")

	decodedTokenID, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	require.Equal(t, tokenID[:], decodedTokenID)
}
