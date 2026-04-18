package commands

import (
	"crypto/rand"
	"encoding/base64"
)

// generateRandomPassword generates a random password of the specified length.
// The password is base64-encoded random bytes, guaranteed to be URL-safe.
func generateRandomPassword(length int) string {
	// Calculate the number of bytes needed (base64 encodes 3 bytes -> 4 chars)
	numBytes := (length * 3) / 4
	if (length * 3) % 4 != 0 {
		numBytes++
	}

	randomBytes := make([]byte, numBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		// Fallback: just use a deterministic string if rand fails (shouldn't happen)
		return "fallback-password-" + base64.RawURLEncoding.EncodeToString(randomBytes[:length])
	}

	// Use URLEncoding which uses - and _ instead of + and /
	encoded := base64.RawURLEncoding.EncodeToString(randomBytes)
	if len(encoded) > length {
		return encoded[:length]
	}
	return encoded
}
