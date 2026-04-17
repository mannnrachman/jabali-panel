package ssokey_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
)

// TestSeal_RoundTrip verifies that plaintext encrypted and then decrypted
// is identical to the original.
func TestSeal_RoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		plaintext string
	}{
		{"empty", ""},
		{"single byte", "x"},
		{"short", "password123"},
		{"long", "this is a much longer password with special chars !@#$%^&*()"},
		{"binary", "\x00\x01\x02\xff\xfe\xfd"},
	}

	// Use a fixed key for reproducibility within the test.
	var key ssokey.Key
	for i := 0; i < 32; i++ {
		key[i] = byte(i)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			envelope, err := key.Seal([]byte(tc.plaintext))
			if err != nil {
				t.Fatalf("Seal failed: %v", err)
			}

			decrypted, err := key.Open(envelope)
			if err != nil {
				t.Fatalf("Open failed: %v", err)
			}

			if !bytes.Equal(decrypted, []byte(tc.plaintext)) {
				t.Errorf("roundtrip failed: got %q, want %q", string(decrypted), tc.plaintext)
			}
		})
	}
}

// TestOpen_TamperedAuthTag verifies that Open detects a corrupted auth tag.
func TestOpen_TamperedAuthTag(t *testing.T) {
	t.Parallel()

	var key ssokey.Key
	for i := 0; i < 32; i++ {
		key[i] = byte(i)
	}

	plaintext := []byte("secret message")
	envelope, err := key.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	// The last byte of the envelope is part of the tag. Flip it.
	if len(envelope) > 0 {
		envelope[len(envelope)-1] ^= 0xFF
	}

	_, err = key.Open(envelope)
	if err == nil {
		t.Error("Open should fail on tampered tag")
	}
}

// TestOpen_TamperedNonce verifies that Open detects a corrupted nonce.
func TestOpen_TamperedNonce(t *testing.T) {
	t.Parallel()

	var key ssokey.Key
	for i := 0; i < 32; i++ {
		key[i] = byte(i)
	}

	plaintext := []byte("secret message")
	envelope, err := key.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	// The first 12 bytes are the nonce. Flip a bit in the first nonce byte.
	if len(envelope) >= 12 {
		envelope[0] ^= 0x01
	}

	_, err = key.Open(envelope)
	if err == nil {
		t.Error("Open should fail on tampered nonce")
	}
}

// TestOpen_TamperedCiphertext verifies that Open detects corrupted ciphertext.
func TestOpen_TamperedCiphertext(t *testing.T) {
	t.Parallel()

	var key ssokey.Key
	for i := 0; i < 32; i++ {
		key[i] = byte(i)
	}

	plaintext := []byte("secret message")
	envelope, err := key.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	// Tamper with a byte in the ciphertext (between nonce and tag).
	// The ciphertext is at least 1 byte if plaintext is non-empty, or 0 bytes
	// for empty plaintext. To ensure we hit a tamper-detectable scenario,
	// only run this if we have room in the ciphertext.
	if len(envelope) > 16 {
		envelope[12] ^= 0x01
		_, err = key.Open(envelope)
		if err == nil {
			t.Error("Open should fail on tampered ciphertext")
		}
	}
}

// TestOpen_ShortEnvelope verifies that Open rejects envelopes shorter than 28 bytes.
func TestOpen_ShortEnvelope(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		length int
	}{
		{"empty", 0},
		{"1 byte", 1},
		{"11 bytes (incomplete nonce)", 11},
		{"12 bytes (nonce only)", 12},
		{"27 bytes (just under min)", 27},
	}

	var key ssokey.Key

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shortEnv := make([]byte, tc.length)
			_, err := key.Open(shortEnv)
			if err == nil {
				t.Error("Open should reject envelope shorter than 28 bytes")
			}
		})
	}
}

// TestOpen_WrongKey verifies that decryption with a different key fails.
func TestOpen_WrongKey(t *testing.T) {
	t.Parallel()

	var key1 ssokey.Key
	for i := 0; i < 32; i++ {
		key1[i] = byte(i)
	}

	var key2 ssokey.Key
	for i := 0; i < 32; i++ {
		key2[i] = byte(i ^ 0xFF)
	}

	plaintext := []byte("secret")
	envelope, err := key1.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	_, err = key2.Open(envelope)
	if err == nil {
		t.Error("Open should fail with wrong key")
	}
}

// TestSeal_NonceUniqueness verifies that calling Seal twice with the same
// plaintext produces envelopes with different nonces (first 12 bytes).
func TestSeal_NonceUniqueness(t *testing.T) {
	t.Parallel()

	var key ssokey.Key
	for i := 0; i < 32; i++ {
		key[i] = byte(i)
	}

	plaintext := []byte("repeated message")

	// Generate multiple envelopes for the same plaintext.
	const attempts = 100
	envelopes := make([][]byte, attempts)
	for i := 0; i < attempts; i++ {
		env, err := key.Seal(plaintext)
		if err != nil {
			t.Fatalf("Seal attempt %d failed: %v", i, err)
		}
		envelopes[i] = env
	}

	// Check that all nonces are unique.
	seenNonces := make(map[string]struct{})
	for i, env := range envelopes {
		nonce := env[:12]
		key := string(nonce)
		if _, dup := seenNonces[key]; dup {
			t.Errorf("Seal attempt %d produced duplicate nonce", i)
		}
		seenNonces[key] = struct{}{}
	}
}

// TestLoad_HappyPath verifies that Load successfully reads a 32-byte key
// from a file.
func TestLoad_HappyPath(t *testing.T) {
	t.Parallel()

	// Create a temporary file with exactly 32 random bytes.
	tmpfile, err := os.CreateTemp("", "ssokey-test-")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	defer os.Remove(tmpfile.Name())

	// Write 32 random bytes.
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatalf("rand.Read failed: %v", err)
	}

	if _, err := tmpfile.Write(keyBytes); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Load the key.
	loadedKey, err := ssokey.Load(tmpfile.Name())
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify the loaded key matches what we wrote.
	if !bytes.Equal(loadedKey[:], keyBytes) {
		t.Error("loaded key does not match written key")
	}
}

// TestLoad_MissingFile verifies that Load returns ErrKeyMissing when the
// file does not exist.
func TestLoad_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := ssokey.Load("/nonexistent/path/to/sso.key")
	if err == nil {
		t.Error("Load should return ErrKeyMissing for missing file")
		return
	}
	if !errors.Is(err, ssokey.ErrKeyMissing) {
		t.Errorf("Load should return ErrKeyMissing for missing file, got %v", err)
	}
}

// TestLoad_WrongSize verifies that Load returns ErrKeyWrongSize when the
// file is not exactly 32 bytes.
func TestLoad_WrongSize(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		size int
	}{
		{"31 bytes", 31},
		{"33 bytes", 33},
		{"0 bytes", 0},
		{"1 byte", 1},
		{"64 bytes", 64},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpfile, err := os.CreateTemp("", "ssokey-test-")
			if err != nil {
				t.Fatalf("CreateTemp failed: %v", err)
			}
			defer os.Remove(tmpfile.Name())

			// Write the wrong number of bytes.
			data := make([]byte, tc.size)
			if _, err := rand.Read(data); err != nil {
				t.Fatalf("rand.Read failed: %v", err)
			}

			if _, err := tmpfile.Write(data); err != nil {
				t.Fatalf("Write failed: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Close failed: %v", err)
			}

			// Load should fail.
			_, err = ssokey.Load(tmpfile.Name())
			if err == nil {
				t.Error("Load should return ErrKeyWrongSize")
				return
			}
			if !errors.Is(err, ssokey.ErrKeyWrongSize) {
				t.Errorf("Load should return ErrKeyWrongSize, got %v", err)
			}
		})
	}
}

// TestLoad_PermissionsAllowed verifies that Load succeeds regardless of
// file permissions. (Permission enforcement is install.sh's responsibility.)
func TestLoad_PermissionsAllowed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mode os.FileMode
	}{
		{"0600 (recommended)", 0o600},
		{"0644 (world readable)", 0o644},
		{"0400 (read-only)", 0o400},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpfile, err := os.CreateTemp("", "ssokey-test-")
			if err != nil {
				t.Fatalf("CreateTemp failed: %v", err)
			}
			defer os.Remove(tmpfile.Name())

			// Write 32 random bytes.
			keyBytes := make([]byte, 32)
			if _, err := rand.Read(keyBytes); err != nil {
				t.Fatalf("rand.Read failed: %v", err)
			}

			if _, err := tmpfile.Write(keyBytes); err != nil {
				t.Fatalf("Write failed: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Close failed: %v", err)
			}

			// Set the desired permissions.
			if err := os.Chmod(tmpfile.Name(), tc.mode); err != nil {
				t.Fatalf("Chmod failed: %v", err)
			}

			// Load should succeed.
			_, err = ssokey.Load(tmpfile.Name())
			if err != nil {
				t.Errorf("Load failed with mode %v: %v", tc.mode, err)
			}
		})
	}
}
