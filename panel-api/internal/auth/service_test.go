package auth_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// In-memory fakes live in this file so we can test AuthService without
// spinning up a DB. The real repositories are covered by sqlmock + integration
// tests elsewhere.

type fakeUserRepo struct {
	mu       sync.Mutex
	byEmail  map[string]*models.User
	byID     map[string]*models.User
	createCB func(*models.User)
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{byEmail: map[string]*models.User{}, byID: map[string]*models.User{}}
}

func (f *fakeUserRepo) Create(_ context.Context, u *models.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.byEmail[u.Email]; dup {
		return repository.ErrConflict
	}
	c := *u
	f.byEmail[u.Email] = &c
	f.byID[u.ID] = &c
	if f.createCB != nil {
		f.createCB(&c)
	}
	return nil
}
func (f *fakeUserRepo) FindByID(_ context.Context, id string) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.byID[id]; ok {
		c := *u
		return &c, nil
	}
	return nil, repository.ErrNotFound
}
func (f *fakeUserRepo) FindByEmail(_ context.Context, email string) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.byEmail[email]; ok {
		c := *u
		return &c, nil
	}
	return nil, repository.ErrNotFound
}

func (f *fakeUserRepo) FindByUsername(_ context.Context, username string) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.byID {
		if u.Username != nil && *u.Username == username {
			c := *u
			return &c, nil
		}
	}
	return nil, repository.ErrNotFound
}
func (f *fakeUserRepo) List(context.Context, int, int) ([]models.User, int64, error) {
	return nil, 0, nil
}
func (f *fakeUserRepo) Update(context.Context, *models.User) error   { return nil }
func (f *fakeUserRepo) SetAdmin(context.Context, string, bool) error { return nil }
func (f *fakeUserRepo) CountAdmins(context.Context) (int64, error)   { return 0, nil }
func (f *fakeUserRepo) Delete(context.Context, string) error         { return nil }

type fakeTokenRepo struct {
	mu     sync.Mutex
	byHash map[string]*models.RefreshToken
}

func newFakeTokenRepo() *fakeTokenRepo {
	return &fakeTokenRepo{byHash: map[string]*models.RefreshToken{}}
}

func (f *fakeTokenRepo) Create(_ context.Context, t *models.RefreshToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.byHash[t.TokenHash]; dup {
		return repository.ErrConflict
	}
	c := *t
	f.byHash[c.TokenHash] = &c
	return nil
}
func (f *fakeTokenRepo) FindByHash(_ context.Context, h string) (*models.RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.byHash[h]; ok {
		c := *t
		return &c, nil
	}
	return nil, repository.ErrNotFound
}
func (f *fakeTokenRepo) Revoke(_ context.Context, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.byHash {
		if t.ID == id && t.RevokedAt == nil {
			x := at
			t.RevokedAt = &x
			return nil
		}
	}
	return repository.ErrNotFound
}
func (f *fakeTokenRepo) RevokeAllForUser(context.Context, string, time.Time) error { return nil }
func (f *fakeTokenRepo) Rotate(_ context.Context, oldHash string, newTok *models.RefreshToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	old, ok := f.byHash[oldHash]
	if !ok || old.RevokedAt != nil {
		return repository.ErrNotFound
	}
	now := time.Now().UTC()
	old.RevokedAt = &now
	c := *newTok
	f.byHash[c.TokenHash] = &c
	return nil
}

// ----- suite setup -----

func newService(t *testing.T) (*auth.Service, *fakeUserRepo, *fakeTokenRepo) {
	t.Helper()
	users := newFakeUserRepo()
	tokens := newFakeTokenRepo()

	iss, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret: []byte("this-secret-is-over-thirty-two-bytes"),
		Issuer: "jabali-panel-test", KeyID: "v1",
		AccessTTL: 15 * time.Minute,
	})
	require.NoError(t, err)

	s := auth.NewService(auth.ServiceConfig{
		Users:       users,
		RefreshRepo: tokens,
		JWT:         iss,
		BcryptCost:  testCost,
		RefreshTTL:  24 * time.Hour,
	})
	return s, users, tokens
}

func seedUser(t *testing.T, users *fakeUserRepo, email, password string, admin bool) *models.User {
	t.Helper()
	hash, err := auth.HashPassword(password, testCost)
	require.NoError(t, err)
	u := &models.User{
		ID: ids.NewULID(), Email: email,
		PasswordHash: hash, IsAdmin: admin,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, users.Create(context.Background(), u))
	return u
}

// ----- tests -----

func TestLogin_Success(t *testing.T) {
	t.Parallel()
	s, users, tokens := newService(t)
	u := seedUser(t, users, "alice@example.com", "hunter2", true)

	out, err := s.Login(context.Background(), auth.LoginInput{
		Email: "alice@example.com", Password: "hunter2", DeviceID: "dev-1",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, out.AccessToken, "access token expected")
	assert.NotEmpty(t, out.RawRefresh, "raw refresh expected")
	assert.Equal(t, u.ID, out.User.ID)

	// Refresh must be stored hashed, never raw.
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	var found bool
	for _, rt := range tokens.byHash {
		if rt.UserID == u.ID {
			assert.NotEqual(t, out.RawRefresh, rt.TokenHash, "never store raw refresh")
			assert.Equal(t, auth.HashRefreshToken(out.RawRefresh), rt.TokenHash)
			found = true
		}
	}
	assert.True(t, found, "refresh row must exist")
}

func TestLogin_WrongPasswordReturnsGenericError(t *testing.T) {
	t.Parallel()
	s, users, _ := newService(t)
	seedUser(t, users, "alice@example.com", "hunter2", false)

	_, err := s.Login(context.Background(), auth.LoginInput{
		Email: "alice@example.com", Password: "WRONG", DeviceID: "d",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidCredentials)
}

func TestLogin_UnknownEmailReturnsGenericError(t *testing.T) {
	t.Parallel()
	s, _, _ := newService(t)

	_, err := s.Login(context.Background(), auth.LoginInput{
		Email: "ghost@example.com", Password: "whatever", DeviceID: "d",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidCredentials,
		"response must be identical to wrong-password so emails can't be enumerated")
}

func TestRefresh_RotatesTokens(t *testing.T) {
	t.Parallel()
	s, users, tokens := newService(t)
	seedUser(t, users, "alice@example.com", "hunter2", false)

	login, err := s.Login(context.Background(), auth.LoginInput{
		Email: "alice@example.com", Password: "hunter2", DeviceID: "d",
	})
	require.NoError(t, err)

	refreshed, err := s.Refresh(context.Background(), auth.RefreshInput{
		RawRefresh: login.RawRefresh, DeviceID: "d",
	})
	require.NoError(t, err)
	assert.NotEqual(t, login.AccessToken, refreshed.AccessToken)
	assert.NotEqual(t, login.RawRefresh, refreshed.RawRefresh)

	// Old hash must now be marked revoked in the fake.
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	old := tokens.byHash[auth.HashRefreshToken(login.RawRefresh)]
	require.NotNil(t, old)
	assert.NotNil(t, old.RevokedAt)
}

func TestRefresh_RejectsReusedOldToken(t *testing.T) {
	t.Parallel()
	s, users, _ := newService(t)
	seedUser(t, users, "alice@example.com", "hunter2", false)

	login, err := s.Login(context.Background(), auth.LoginInput{
		Email: "alice@example.com", Password: "hunter2", DeviceID: "d",
	})
	require.NoError(t, err)

	_, err = s.Refresh(context.Background(), auth.RefreshInput{RawRefresh: login.RawRefresh, DeviceID: "d"})
	require.NoError(t, err)

	// Replay the old refresh — must fail.
	_, err = s.Refresh(context.Background(), auth.RefreshInput{RawRefresh: login.RawRefresh, DeviceID: "d"})
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestRefresh_RejectsExpired(t *testing.T) {
	t.Parallel()
	users := newFakeUserRepo()
	tokens := newFakeTokenRepo()
	iss, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret: []byte("this-secret-is-over-thirty-two-bytes"),
		Issuer: "t", KeyID: "v1", AccessTTL: time.Minute,
	})
	require.NoError(t, err)
	s := auth.NewService(auth.ServiceConfig{
		Users: users, RefreshRepo: tokens, JWT: iss,
		BcryptCost: testCost,
		RefreshTTL: -1 * time.Minute, // issued-expired refresh
	})
	seedUser(t, users, "alice@example.com", "hunter2", false)

	login, err := s.Login(context.Background(), auth.LoginInput{
		Email: "alice@example.com", Password: "hunter2", DeviceID: "d",
	})
	require.NoError(t, err)

	_, err = s.Refresh(context.Background(), auth.RefreshInput{RawRefresh: login.RawRefresh, DeviceID: "d"})
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestLogout_RevokesRefreshToken(t *testing.T) {
	t.Parallel()
	s, users, tokens := newService(t)
	seedUser(t, users, "alice@example.com", "hunter2", false)

	login, err := s.Login(context.Background(), auth.LoginInput{
		Email: "alice@example.com", Password: "hunter2", DeviceID: "d",
	})
	require.NoError(t, err)

	require.NoError(t, s.Logout(context.Background(), login.RawRefresh))

	// Replay refresh — revoked, rejected.
	_, err = s.Refresh(context.Background(), auth.RefreshInput{RawRefresh: login.RawRefresh, DeviceID: "d"})
	require.Error(t, err)

	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	row := tokens.byHash[auth.HashRefreshToken(login.RawRefresh)]
	require.NotNil(t, row)
	assert.NotNil(t, row.RevokedAt)
}

func TestLogout_UnknownTokenIsIdempotent(t *testing.T) {
	t.Parallel()
	s, _, _ := newService(t)
	// Not an error if the client sends a stale cookie — logout is best-effort
	// from the user's perspective.
	assert.NoError(t, s.Logout(context.Background(), "does-not-exist"))
}
