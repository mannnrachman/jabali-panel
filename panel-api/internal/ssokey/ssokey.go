// Package ssokey provides AES-256-GCM envelope encryption for the phpMyAdmin
// SSO shadow account password. The password is encrypted at rest in the
// database and decrypted only when needed to populate the SSO token.
package ssokey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
)

// Key is a 32-byte AES-256 key, treated as a value type.
type Key [32]byte

// ErrKeyMissing is returned by Load when the key file does not exist.
var ErrKeyMissing = errors.New("sso key file not found")

// ErrKeyWrongSize is returned by Load when the key file is not exactly 32 bytes.
var ErrKeyWrongSize = errors.New("sso key must be exactly 32 bytes")

// Load reads exactly 32 bytes from path. Returns ErrKeyMissing if the file
// does not exist, ErrKeyWrongSize if the file is not exactly 32 bytes.
// Any other I/O error is wrapped and returned as-is.
func Load(path string) (Key, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Key{}, ErrKeyMissing
		}
		return Key{}, fmt.Errorf("open sso key file: %w", err)
	}
	defer f.Close()

	var k Key
	n, err := io.ReadFull(f, k[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return Key{}, fmt.Errorf("read sso key: %w", err)
	}

	// ReadFull returns io.ErrUnexpectedEOF if fewer than 32 bytes are available.
	if n != 32 {
		return Key{}, ErrKeyWrongSize
	}

	// Verify we're at EOF — file must be exactly 32 bytes, no more.
	var b [1]byte
	if _, err := f.Read(b[:]); err != io.EOF {
		return Key{}, ErrKeyWrongSize
	}

	return k, nil
}

// Seal encrypts plaintext using AES-256-GCM and returns nonce(12) || ciphertext || tag(16).
// Every call generates a fresh 12-byte nonce via crypto/rand.Read.
// Returns an error if nonce generation or encryption fails.
func (k Key) Seal(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	// Seal returns ciphertext || tag. Prepend nonce.
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	envelope := make([]byte, 0, 12+len(ciphertext))
	envelope = append(envelope, nonce...)
	envelope = append(envelope, ciphertext...)

	return envelope, nil
}

// Open decrypts an envelope created by Seal. The envelope must be in the form
// nonce(12) || ciphertext || tag(16).
// Returns an error if the envelope is shorter than 28 bytes or if the auth tag
// fails verification. Does not panic on tag mismatch.
func (k Key) Open(envelope []byte) ([]byte, error) {
	// Minimum: 12 (nonce) + 16 (tag) = 28 bytes. With ciphertext, >28.
	if len(envelope) < 28 {
		return nil, errors.New("envelope too short")
	}

	nonce := envelope[:12]
	ciphertext := envelope[12:]

	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	return plaintext, nil
}

// verify is a compile-time check that Key is exactly 32 bytes.
var _ [32]byte = Key{}
