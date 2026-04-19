package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// fakeKratos implements auth.KratosIdentityWriter for bootstrap tests.
// Captures calls so assertions can inspect the exact request payload the
// bootstrap path produces (email, is_admin, hash prefix).
type fakeKratos struct {
	createdID         string
	createErr         error
	deleteCalled      bool
	deleteErr         error
	lastTraits        kratosclient.AdminTraits
	lastPasswordHash  string
	lastDeleteID      string
}

func (f *fakeKratos) CreateIdentityWithPassword(_ context.Context, traits kratosclient.AdminTraits, hash string) (string, error) {
	f.lastTraits = traits
	f.lastPasswordHash = hash
	if f.createErr != nil {
		return "", f.createErr
	}
	return f.createdID, nil
}

func (f *fakeKratos) DeleteIdentity(_ context.Context, id string) error {
	f.deleteCalled = true
	f.lastDeleteID = id
	return f.deleteErr
}

func TestBootstrapAdmin_Kratos_CreatesIdentityAndLinks(t *testing.T) {
	t.Parallel()
	users := newFakeUserRepo()
	k := &fakeKratos{createdID: "kratos-uuid-abc"}

	res, err := auth.BootstrapAdmin(context.Background(), users, auth.BootstrapOptions{
		Email: "admin@example.com", Password: "strongpass", BcryptCost: testCost,
		Kratos: k,
	})
	require.NoError(t, err)
	assert.True(t, res.Created)
	assert.Equal(t, "kratos-uuid-abc", res.KratosIdentityID,
		"BootstrapResult must surface the Kratos identity so install.sh logs can reference it")

	// Traits passed to Kratos must reflect the admin role (is_admin=true,
	// exactly email/username from the panel row).
	assert.Equal(t, "admin@example.com", k.lastTraits.Email)
	assert.True(t, k.lastTraits.IsAdmin, "bootstrap admin always has is_admin=true")

	// Panel row must be updated with the kratos_identity_id so the
	// middleware can resolve Kratos UUIDs → panel ULID on first login.
	u, err := users.FindByEmail(context.Background(), "admin@example.com")
	require.NoError(t, err)
	require.NotNil(t, u.KratosIdentityID, "panel row must be linked after bootstrap")
	assert.Equal(t, "kratos-uuid-abc", *u.KratosIdentityID)
	assert.True(t, auth.VerifyPassword(u.PasswordHash, "strongpass"),
		"bcrypt passthrough — same hash lives in both systems")
}

func TestBootstrapAdmin_Kratos_RollsBackPanelOnKratosFailure(t *testing.T) {
	t.Parallel()
	users := newFakeUserRepo()
	k := &fakeKratos{createErr: errors.New("kratos 500: schema violation")}

	res, err := auth.BootstrapAdmin(context.Background(), users, auth.BootstrapOptions{
		Email: "admin@example.com", Password: "strongpass", BcryptCost: testCost,
		Kratos: k,
	})
	require.Error(t, err, "kratos failure must propagate")
	assert.False(t, res.Created, "no row should remain for a failed atomic insert")
	assert.Contains(t, err.Error(), "create kratos identity")

	// Panel row must be rolled back — the whole point of the compensating
	// transaction. Subsequent retries must succeed, so a ghost row
	// from the first attempt can't linger.
	_, findErr := users.FindByEmail(context.Background(), "admin@example.com")
	assert.ErrorIs(t, findErr, repository.ErrNotFound,
		"panel row must be deleted when kratos create fails")
}

func TestBootstrapAdmin_Kratos_IdempotentOnExistingAdmin(t *testing.T) {
	t.Parallel()
	users := newFakeUserRepo()
	// Seed an existing linked admin — simulates a re-run after first-boot
	// already completed. Bootstrap must see it and NOT call Kratos.
	u := seedUser(t, users, "admin@example.com", "originalpw", true)
	kid := "kratos-uuid-existing"
	u.KratosIdentityID = &kid
	require.NoError(t, users.Update(context.Background(), u))

	k := &fakeKratos{createdID: "should-not-be-used"}
	res, err := auth.BootstrapAdmin(context.Background(), users, auth.BootstrapOptions{
		Email: "admin@example.com", Password: "newpw-from-env", BcryptCost: testCost,
		Kratos: k,
	})
	require.NoError(t, err)
	assert.False(t, res.Created)
	assert.NotEmpty(t, res.ExistingID)
	assert.Equal(t, kid, res.KratosIdentityID,
		"existing linkage must surface so operators see current state")

	// fakeKratos tracks whether Create was called — re-bootstrap must be
	// fully idempotent (no duplicate Kratos identity created).
	assert.Empty(t, k.lastTraits.Email, "Kratos.CreateIdentityWithPassword must NOT be called on idempotent re-run")
}

func TestBootstrapAdmin_LegacyMode_DoesNotTouchKratos(t *testing.T) {
	t.Parallel()
	users := newFakeUserRepo()

	res, err := auth.BootstrapAdmin(context.Background(), users, auth.BootstrapOptions{
		Email: "admin@example.com", Password: "strongpass", BcryptCost: testCost,
		// Kratos: nil — legacy mode
	})
	require.NoError(t, err)
	assert.True(t, res.Created)
	assert.Empty(t, res.KratosIdentityID, "legacy bootstrap must NOT populate KratosIdentityID")

	u, err := users.FindByEmail(context.Background(), "admin@example.com")
	require.NoError(t, err)
	assert.Nil(t, u.KratosIdentityID, "panel row's kratos_identity_id must be NULL in legacy mode")
}
