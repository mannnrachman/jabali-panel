package magiclink

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoad_Success verifies successful key loading.
func TestLoad_Success(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "magic-link.key")

	// Create a valid key (32 random bytes encoded as base64url)
	key1 := Key{}
	for i := 0; i < 32; i++ {
		key1[i] = byte(i)
	}
	key1Encoded := base64.RawURLEncoding.EncodeToString(key1[:])

	// Create a second key for rotation testing
	key2 := Key{}
	for i := 0; i < 32; i++ {
		key2[i] = byte(i + 32)
	}
	key2Encoded := base64.RawURLEncoding.EncodeToString(key2[:])

	// Write file with two keys (newest first)
	keyContent := key1Encoded + "," + key2Encoded
	err := os.WriteFile(keyPath, []byte(keyContent), 0600)
	require.NoError(t, err)

	// Load keys
	keys, err := Load(keyPath)
	require.NoError(t, err)
	require.NotNil(t, keys)

	// Verify Signing() returns the first key
	signingKey := keys.Signing()
	require.Equal(t, key1, signingKey)

	// Verify All() returns both keys in order
	allKeys := keys.All()
	require.Len(t, allKeys, 2)
	require.Equal(t, key1, allKeys[0])
	require.Equal(t, key2, allKeys[1])
}

// TestLoad_MissingFile verifies ErrKeyMissing when file doesn't exist.
func TestLoad_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "nonexistent.key")

	keys, err := Load(keyPath)
	require.Error(t, err)
	require.Nil(t, keys)
	require.ErrorIs(t, err, ErrKeyMissing)
}

// TestLoad_BadPermissions verifies ErrKeyBadMode when file permissions are incorrect.
func TestLoad_BadPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "magic-link.key")

	key := Key{}
	for i := 0; i < 32; i++ {
		key[i] = byte(i)
	}
	keyEncoded := base64.RawURLEncoding.EncodeToString(key[:])

	// Write file with incorrect permissions (0644 instead of 0600)
	err := os.WriteFile(keyPath, []byte(keyEncoded), 0644)
	require.NoError(t, err)

	keys, err := Load(keyPath)
	require.Error(t, err)
	require.Nil(t, keys)
	require.ErrorIs(t, err, ErrKeyBadMode)
}

// TestLoad_EmptyFile verifies ErrKeyMalformed when file is empty.
func TestLoad_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "magic-link.key")

	// Write empty file
	err := os.WriteFile(keyPath, []byte(""), 0600)
	require.NoError(t, err)

	keys, err := Load(keyPath)
	require.Error(t, err)
	require.Nil(t, keys)
	require.ErrorIs(t, err, ErrKeyMalformed)
}

// TestLoad_InvalidBase64 verifies ErrKeyMalformed for invalid base64url.
func TestLoad_InvalidBase64(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "magic-link.key")

	// Write invalid base64url (contains invalid characters)
	err := os.WriteFile(keyPath, []byte("!!!not-valid-base64!!!"), 0600)
	require.NoError(t, err)

	keys, err := Load(keyPath)
	require.Error(t, err)
	require.Nil(t, keys)
	require.ErrorIs(t, err, ErrKeyMalformed)
}

// TestLoad_WrongKeyLength verifies ErrKeyMalformed for incorrect key length.
func TestLoad_WrongKeyLength(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "magic-link.key")

	// Create a 16-byte key instead of 32
	var shortKey [16]byte
	for i := 0; i < 16; i++ {
		shortKey[i] = byte(i)
	}
	shortKeyEncoded := base64.RawURLEncoding.EncodeToString(shortKey[:])

	err := os.WriteFile(keyPath, []byte(shortKeyEncoded), 0600)
	require.NoError(t, err)

	keys, err := Load(keyPath)
	require.Error(t, err)
	require.Nil(t, keys)
	require.ErrorIs(t, err, ErrKeyMalformed)
}

// TestLoad_EmptyKeyInList verifies ErrKeyMalformed for empty key in comma-separated list.
func TestLoad_EmptyKeyInList(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "magic-link.key")

	key := Key{}
	for i := 0; i < 32; i++ {
		key[i] = byte(i)
	}
	keyEncoded := base64.RawURLEncoding.EncodeToString(key[:])

	// Write with empty key in the list
	err := os.WriteFile(keyPath, []byte(keyEncoded+","+""), 0600)
	require.NoError(t, err)

	keys, err := Load(keyPath)
	require.Error(t, err)
	require.Nil(t, keys)
	require.ErrorIs(t, err, ErrKeyMalformed)
}

// TestLoad_KeyRotation verifies that keys are stored in correct order (newest first).
func TestLoad_KeyRotation(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "magic-link.key")

	// Create three keys with different contents
	key1 := Key{}
	for i := 0; i < 32; i++ {
		key1[i] = byte(i)
	}

	key2 := Key{}
	for i := 0; i < 32; i++ {
		key2[i] = byte(i + 32)
	}

	key3 := Key{}
	for i := 0; i < 32; i++ {
		key3[i] = byte(i + 64)
	}

	// Write in rotation order: newest (key1), middle (key2), oldest (key3)
	keyContent := base64.RawURLEncoding.EncodeToString(key1[:]) + "," +
		base64.RawURLEncoding.EncodeToString(key2[:]) + "," +
		base64.RawURLEncoding.EncodeToString(key3[:])
	err := os.WriteFile(keyPath, []byte(keyContent), 0600)
	require.NoError(t, err)

	keys, err := Load(keyPath)
	require.NoError(t, err)

	// Verify Signing() returns key1 (newest)
	require.Equal(t, key1, keys.Signing())

	// Verify All() returns all keys in order
	allKeys := keys.All()
	require.Len(t, allKeys, 3)
	require.Equal(t, key1, allKeys[0])
	require.Equal(t, key2, allKeys[1])
	require.Equal(t, key3, allKeys[2])
}

// TestLoad_WithWhitespace verifies that whitespace is handled correctly.
func TestLoad_WithWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "magic-link.key")

	key := Key{}
	for i := 0; i < 32; i++ {
		key[i] = byte(i)
	}
	keyEncoded := base64.RawURLEncoding.EncodeToString(key[:])

	// Write with leading/trailing whitespace
	keyContent := "  " + keyEncoded + "  \n"
	err := os.WriteFile(keyPath, []byte(keyContent), 0600)
	require.NoError(t, err)

	keys, err := Load(keyPath)
	require.NoError(t, err)
	require.NotNil(t, keys)

	// Verify the key was parsed correctly
	require.Equal(t, key, keys.Signing())
}

// TestLoad_KeysWithInternalWhitespace verifies whitespace around comma-separated keys.
func TestLoad_KeysWithInternalWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "magic-link.key")

	key1 := Key{}
	for i := 0; i < 32; i++ {
		key1[i] = byte(i)
	}

	key2 := Key{}
	for i := 0; i < 32; i++ {
		key2[i] = byte(i + 32)
	}

	key1Encoded := base64.RawURLEncoding.EncodeToString(key1[:])
	key2Encoded := base64.RawURLEncoding.EncodeToString(key2[:])

	// Write with spaces around comma
	keyContent := key1Encoded + " , " + key2Encoded
	err := os.WriteFile(keyPath, []byte(keyContent), 0600)
	require.NoError(t, err)

	keys, err := Load(keyPath)
	require.NoError(t, err)

	allKeys := keys.All()
	require.Len(t, allKeys, 2)
	require.Equal(t, key1, allKeys[0])
	require.Equal(t, key2, allKeys[1])
}
