package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// minimal fakes — only the two interfaces disable2FA actually uses.

type fake2FAUserRepo struct {
	users map[string]*models.User
}

func (f *fake2FAUserRepo) Create(context.Context, *models.User) error { return nil }
func (f *fake2FAUserRepo) FindByID(_ context.Context, id string) (*models.User, error) {
	if u, ok := f.users[id]; ok {
		return u, nil
	}
	return nil, repository.ErrNotFound
}
func (f *fake2FAUserRepo) FindByEmail(_ context.Context, email string) (*models.User, error) {
	for _, u := range f.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, repository.ErrNotFound
}
func (f *fake2FAUserRepo) FindByUsername(context.Context, string) (*models.User, error) {
	return nil, repository.ErrNotFound
}
func (f *fake2FAUserRepo) FindByKratosIdentityID(context.Context, string) (*models.User, error) {
	return nil, repository.ErrNotFound
}
func (f *fake2FAUserRepo) List(context.Context, repository.ListOptions) ([]models.User, int64, error) {
	return nil, 0, nil
}
func (f *fake2FAUserRepo) Update(context.Context, *models.User) error         { return nil }
func (f *fake2FAUserRepo) SetAdmin(context.Context, string, bool) error       { return nil }
func (f *fake2FAUserRepo) CountAdmins(context.Context) (int64, error)         { return 0, nil }
func (f *fake2FAUserRepo) FindAdminsByEmail(context.Context) ([]*models.User, error) {
	return nil, nil
}
func (f *fake2FAUserRepo) Delete(context.Context, string) error { return nil }
func (f *fake2FAUserRepo) SetTOTPSecret(_ context.Context, id string, enc []byte) error {
	if u, ok := f.users[id]; ok {
		u.TOTPSecretEncrypted = enc
	}
	return nil
}
func (f *fake2FAUserRepo) EnableTOTP(_ context.Context, id string, now time.Time) error {
	if u, ok := f.users[id]; ok {
		u.TOTPEnabled = true
		u.TOTPEnabledAt = &now
	}
	return nil
}
func (f *fake2FAUserRepo) DisableTOTP(_ context.Context, id string) error {
	if u, ok := f.users[id]; ok {
		u.TOTPEnabled = false
		u.TOTPEnabledAt = nil
		u.TOTPSecretEncrypted = nil
	}
	return nil
}

type fake2FABackupRepo struct {
	byUser map[string][]models.TOTPBackupCode
}

func (f *fake2FABackupRepo) CreateBatch(_ context.Context, codes []models.TOTPBackupCode) error {
	for _, c := range codes {
		f.byUser[c.UserID] = append(f.byUser[c.UserID], c)
	}
	return nil
}
func (f *fake2FABackupRepo) ListUnusedByUserID(_ context.Context, userID string) ([]models.TOTPBackupCode, error) {
	return f.byUser[userID], nil
}
func (f *fake2FABackupRepo) MarkUsed(context.Context, string, time.Time) error { return nil }
func (f *fake2FABackupRepo) DeleteAllByUserID(_ context.Context, userID string) error {
	delete(f.byUser, userID)
	return nil
}

// --- tests ---

func TestDisable2FA_HappyPath(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	users := &fake2FAUserRepo{users: map[string]*models.User{
		"u1": {
			ID:                  "u1",
			Email:               "alice@example.com",
			TOTPEnabled:         true,
			TOTPEnabledAt:       &now,
			TOTPSecretEncrypted: []byte("sealed"),
		},
	}}
	backups := &fake2FABackupRepo{byUser: map[string][]models.TOTPBackupCode{
		"u1": {{ID: "b1", UserID: "u1", CodeHash: "x"}, {ID: "b2", UserID: "u1", CodeHash: "y"}},
	}}

	err := disable2FA(context.Background(), users, backups, slog.Default(), "alice@example.com")
	require.NoError(t, err)

	u := users.users["u1"]
	assert.False(t, u.TOTPEnabled)
	assert.Nil(t, u.TOTPEnabledAt)
	assert.Nil(t, u.TOTPSecretEncrypted)
	assert.Empty(t, backups.byUser["u1"], "backup codes must be wiped")
}

func TestDisable2FA_UnknownEmail(t *testing.T) {
	t.Parallel()
	users := &fake2FAUserRepo{users: map[string]*models.User{}}
	backups := &fake2FABackupRepo{byUser: map[string][]models.TOTPBackupCode{}}

	err := disable2FA(context.Background(), users, backups, slog.Default(), "ghost@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no user with email")
}

func TestDisable2FA_AlreadyDisabled_NoOp(t *testing.T) {
	t.Parallel()
	users := &fake2FAUserRepo{users: map[string]*models.User{
		"u1": {ID: "u1", Email: "alice@example.com", TOTPEnabled: false},
	}}
	backups := &fake2FABackupRepo{byUser: map[string][]models.TOTPBackupCode{}}

	err := disable2FA(context.Background(), users, backups, slog.Default(), "alice@example.com")
	require.NoError(t, err)
	// User row unchanged.
	u := users.users["u1"]
	assert.False(t, u.TOTPEnabled)
	assert.Nil(t, u.TOTPSecretEncrypted)
}
