package auth_test

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestGenerateRefreshToken_ReturnsHexPair(t *testing.T) {
	t.Parallel()

	raw, hash, err := auth.GenerateRefreshToken()
	require.NoError(t, err)
	assert.True(t, hex64.MatchString(raw), "raw not 64 lower-hex: %q", raw)
	assert.True(t, hex64.MatchString(hash), "hash not 64 lower-hex: %q", hash)

	// hash must be SHA256(raw) in hex
	want := sha256.Sum256([]byte(raw))
	assert.Equal(t, hex.EncodeToString(want[:]), hash)
}

func TestGenerateRefreshToken_NewEachCall(t *testing.T) {
	t.Parallel()
	a, _, err := auth.GenerateRefreshToken()
	require.NoError(t, err)
	b, _, err := auth.GenerateRefreshToken()
	require.NoError(t, err)
	assert.NotEqual(t, a, b)
}

func TestHashRefreshToken_Deterministic(t *testing.T) {
	t.Parallel()
	got := auth.HashRefreshToken("abc")
	// sha256("abc") = ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad
	assert.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", got)
}

func TestDeriveDeviceID_UsesHeaderWhenSet(t *testing.T) {
	t.Parallel()
	id := auth.DeriveDeviceID("my-device-123", "Mozilla/5.0", "203.0.113.1")
	assert.Equal(t, "my-device-123", id)
}

func TestDeriveDeviceID_FallsBackToHash(t *testing.T) {
	t.Parallel()
	id := auth.DeriveDeviceID("", "Mozilla/5.0 (X11)", "203.0.113.1")
	// Falls back to sha256 of UA + IP — some 64-char hex string.
	assert.True(t, hex64.MatchString(id))
	// Same inputs → same ID.
	assert.Equal(t, id, auth.DeriveDeviceID("", "Mozilla/5.0 (X11)", "203.0.113.1"))
	// Different inputs → different ID.
	assert.NotEqual(t, id, auth.DeriveDeviceID("", "curl/8.0", "203.0.113.1"))
}
