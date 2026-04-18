package sshkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestParseAndFingerprint_ValidED25519(t *testing.T) {
	// Generate a valid ed25519 key
	pubED25519, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	sshPubKey, err := ssh.NewPublicKey(pubED25519)
	require.NoError(t, err)

	authorizedKeyBytes := ssh.MarshalAuthorizedKey(sshPubKey)

	normalizedKey, fingerprint, err := ParseAndFingerprint(string(authorizedKeyBytes))
	require.NoError(t, err)
	require.NotEmpty(t, normalizedKey)
	require.NotEmpty(t, fingerprint)
	require.True(t, len(fingerprint) <= 64)
}

func TestParseAndFingerprint_ValidRSA2048(t *testing.T) {
	// Generate a valid RSA 2048 key
	privRSA, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	sshPubKey, err := ssh.NewPublicKey(&privRSA.PublicKey)
	require.NoError(t, err)

	authorizedKeyBytes := ssh.MarshalAuthorizedKey(sshPubKey)

	normalizedKey, fingerprint, err := ParseAndFingerprint(string(authorizedKeyBytes))
	require.NoError(t, err)
	require.NotEmpty(t, normalizedKey)
	require.NotEmpty(t, fingerprint)
	require.True(t, len(fingerprint) <= 64)
}

func TestParseAndFingerprint_RSA1024_TooWeak(t *testing.T) {
	// Generate RSA 1024 (too weak)
	privRSA, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(t, err)

	sshPubKey, err := ssh.NewPublicKey(&privRSA.PublicKey)
	require.NoError(t, err)

	authorizedKeyBytes := ssh.MarshalAuthorizedKey(sshPubKey)

	_, _, err = ParseAndFingerprint(string(authorizedKeyBytes))
	require.Error(t, err)
	require.Equal(t, ErrRSATooWeak, err)
}

func TestParseAndFingerprint_InvalidKeyFormat(t *testing.T) {
	_, _, err := ParseAndFingerprint("garbage text that is not a key")
	require.Error(t, err)
	require.Equal(t, ErrInvalidKeyFormat, err)
}

func TestParseAndFingerprint_EmptyString(t *testing.T) {
	_, _, err := ParseAndFingerprint("")
	require.Error(t, err)
	require.Equal(t, ErrInvalidKeyFormat, err)
}

func TestParseAndFingerprint_WithWhitespace(t *testing.T) {
	// Generate a valid ed25519 key
	pubED25519, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	sshPubKey, err := ssh.NewPublicKey(pubED25519)
	require.NoError(t, err)

	authorizedKeyBytes := ssh.MarshalAuthorizedKey(sshPubKey)

	// Add leading/trailing whitespace
	keyWithWhitespace := "  " + string(authorizedKeyBytes) + "  "

	normalizedKey, fingerprint, err := ParseAndFingerprint(keyWithWhitespace)
	require.NoError(t, err)
	require.NotEmpty(t, normalizedKey)
	require.NotEmpty(t, fingerprint)
}
