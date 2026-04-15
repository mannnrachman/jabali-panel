package auth_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
)

func TestBootstrapAdmin_CreatesWhenMissing(t *testing.T) {
	t.Parallel()
	users := newFakeUserRepo()

	res, err := auth.BootstrapAdmin(context.Background(), users, auth.BootstrapOptions{
		Email: "admin@example.com", Password: "strongpass", BcryptCost: testCost,
	})
	require.NoError(t, err)
	assert.True(t, res.Created)

	// User was actually inserted as admin with hashed password.
	u, err := users.FindByEmail(context.Background(), "admin@example.com")
	require.NoError(t, err)
	assert.True(t, u.IsAdmin)
	assert.True(t, auth.VerifyPassword(u.PasswordHash, "strongpass"))
}

func TestBootstrapAdmin_SkipsWhenEnvEmpty(t *testing.T) {
	t.Parallel()
	users := newFakeUserRepo()

	res, err := auth.BootstrapAdmin(context.Background(), users, auth.BootstrapOptions{
		Email: "", Password: "", BcryptCost: testCost,
	})
	require.NoError(t, err)
	assert.True(t, res.SkippedEmpty)
	assert.False(t, res.Created)
}

func TestBootstrapAdmin_IdempotentWhenAlreadyAdmin(t *testing.T) {
	t.Parallel()
	users := newFakeUserRepo()
	seedUser(t, users, "admin@example.com", "originalpw", true)

	// Re-running with a different password should NOT overwrite — safety
	// against a leaked env var blowing away the operator credential.
	res, err := auth.BootstrapAdmin(context.Background(), users, auth.BootstrapOptions{
		Email: "admin@example.com", Password: "newpw-from-env", BcryptCost: testCost,
	})
	require.NoError(t, err)
	assert.False(t, res.Created)
	assert.NotEmpty(t, res.ExistingID)

	u, err := users.FindByEmail(context.Background(), "admin@example.com")
	require.NoError(t, err)
	assert.True(t, auth.VerifyPassword(u.PasswordHash, "originalpw"), "original password must still work")
	assert.False(t, auth.VerifyPassword(u.PasswordHash, "newpw-from-env"))
}

func TestBootstrapAdmin_RefusesToElevateExistingNonAdmin(t *testing.T) {
	t.Parallel()
	users := newFakeUserRepo()
	seedUser(t, users, "alice@example.com", "hunter2", false)

	_, err := auth.BootstrapAdmin(context.Background(), users, auth.BootstrapOptions{
		Email: "alice@example.com", Password: "upgrade", BcryptCost: testCost,
	})
	require.Error(t, err, "must refuse silent privilege escalation")
}
