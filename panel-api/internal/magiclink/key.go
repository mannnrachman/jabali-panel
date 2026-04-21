package magiclink

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

var (
	// ErrKeyMissing indicates the key file does not exist.
	ErrKeyMissing = errors.New("magic link key file missing")
	// ErrKeyBadMode indicates the key file has incorrect permissions.
	ErrKeyBadMode = errors.New("magic link key file has incorrect permissions (expected 0600)")
	// ErrKeyMalformed indicates the key file content is invalid.
	ErrKeyMalformed = errors.New("magic link key file is malformed")
)

// Key is a 32-byte signing key.
type Key [32]byte

// Keys holds all available keys (newest first for signing, all for verification).
type Keys struct {
	keys []Key
}

// Load reads and parses the magic link key file.
// The file is expected to contain comma-separated base64url-encoded 32-byte keys.
// Boot-time guards:
// - File must exist
// - File permissions must be exactly 0600
// - File content must be valid base64url with 32-byte keys
func Load(path string) (*Keys, error) {
	// Check if file exists
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrKeyMissing
		}
		return nil, fmt.Errorf("failed to stat key file: %w", err)
	}

	// Check file permissions (must be exactly 0600)
	if info.Mode().Perm() != 0600 {
		return nil, fmt.Errorf("%w: got %04o", ErrKeyBadMode, info.Mode().Perm())
	}

	// Read file content
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}

	// Parse comma-separated base64url keys
	keyStrs := strings.Split(strings.TrimSpace(string(content)), ",")
	if len(keyStrs) == 0 {
		return nil, fmt.Errorf("%w: empty key list", ErrKeyMalformed)
	}

	var keys []Key
	for i, keyStr := range keyStrs {
		keyStr = strings.TrimSpace(keyStr)
		if keyStr == "" {
			return nil, fmt.Errorf("%w: empty key at index %d", ErrKeyMalformed, i)
		}

		// Decode base64url
		decoded, err := base64.RawURLEncoding.DecodeString(keyStr)
		if err != nil {
			return nil, fmt.Errorf("%w: key %d is not valid base64url: %w", ErrKeyMalformed, i, err)
		}

		if len(decoded) != 32 {
			return nil, fmt.Errorf("%w: key %d has incorrect length %d (expected 32)", ErrKeyMalformed, i, len(decoded))
		}

		var key Key
		copy(key[:], decoded)
		keys = append(keys, key)
	}

	return &Keys{keys: keys}, nil
}

// Signing returns the signing key (most recent, keys[0]).
func (k *Keys) Signing() Key {
	return k.keys[0]
}

// All returns all available keys for verification.
func (k *Keys) All() []Key {
	return k.keys
}
