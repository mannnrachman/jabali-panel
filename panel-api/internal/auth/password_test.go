package auth_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
)

// Cost 4 keeps password tests sub-second. Production uses the default (12).
const testCost = 4

func TestHash_ProducesValidBcryptString(t *testing.T) {
	t.Parallel()

	h, err := auth.HashPassword("s3cret!", testCost)
	require.NoError(t, err)

	// bcrypt hashes start with $2a$ / $2b$ / $2y$ and encode cost inline.
	assert.True(t, strings.HasPrefix(h, "$2"), "expected bcrypt prefix, got %q", h)
	assert.Len(t, h, 60, "bcrypt hashes are always 60 chars")
}

func TestVerifyPassword_AcceptsCorrect(t *testing.T) {
	t.Parallel()
	h, err := auth.HashPassword("s3cret!", testCost)
	require.NoError(t, err)

	assert.True(t, auth.VerifyPassword(h, "s3cret!"))
}

func TestVerifyPassword_RejectsWrong(t *testing.T) {
	t.Parallel()
	h, err := auth.HashPassword("s3cret!", testCost)
	require.NoError(t, err)

	assert.False(t, auth.VerifyPassword(h, "wrong"))
	assert.False(t, auth.VerifyPassword(h, ""))
	assert.False(t, auth.VerifyPassword(h, "S3cret!"))
}

func TestVerifyPassword_RejectsMalformedHash(t *testing.T) {
	t.Parallel()
	// Garbage where a bcrypt hash should be.
	assert.False(t, auth.VerifyPassword("not-a-hash", "anything"))
	assert.False(t, auth.VerifyPassword("", "anything"))
}

func TestDummyHash_IsValidBcryptAndAlwaysFails(t *testing.T) {
	t.Parallel()
	// auth.DummyHash must be a pre-computed bcrypt hash so VerifyPassword
	// takes the same time as a real hash compare — mitigates email
	// enumeration via timing.
	assert.True(t, strings.HasPrefix(auth.DummyHash, "$2"))
	assert.Len(t, auth.DummyHash, 60)
	// It is deliberately a hash of a value callers cannot know, so no
	// password should ever verify against it.
	assert.False(t, auth.VerifyPassword(auth.DummyHash, ""))
	assert.False(t, auth.VerifyPassword(auth.DummyHash, "password"))
}

func TestHash_RejectsEmptyPassword(t *testing.T) {
	t.Parallel()
	_, err := auth.HashPassword("", testCost)
	require.Error(t, err)
}
