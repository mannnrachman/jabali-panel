package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func TestRefreshTokenRepository_Create(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewRefreshTokenRepository(gdb)

	tok := &models.RefreshToken{
		ID:       "01HRCWR7CKMCBEDF2PYQ7G0D2K",
		UserID:   "01HRCWR7CKMCBEDF2PYQ7G0D2J",
		DeviceID: "dev-1",
		TokenHash: "deadbeef" + // 64 hex chars total
			"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
		CreatedAt: time.Now().UTC(),
	}

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .refresh_tokens.`).
		WithArgs(
			tok.ID, tok.UserID, tok.DeviceID, tok.TokenHash,
			tok.ExpiresAt, nil, nil, nil, sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.Create(context.Background(), tok)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshTokenRepository_Rotate_HappyPath(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewRefreshTokenRepository(gdb)

	oldHash := "old-hash"
	newTok := &models.RefreshToken{
		ID:        "01HRCWR7CKMCBEDF2PYQ7G0D2M",
		UserID:    "01HRCWR7CKMCBEDF2PYQ7G0D2J",
		DeviceID:  "dev-1",
		TokenHash: "new-hash",
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
		CreatedAt: time.Now().UTC(),
	}

	now := time.Now().UTC()
	mock.ExpectBegin()
	// SELECT ... FOR UPDATE returns the existing un-revoked row.
	mock.ExpectQuery(`SELECT \* FROM .refresh_tokens. WHERE token_hash = \? ORDER BY .refresh_tokens.\..id. LIMIT \? FOR UPDATE`).
		WithArgs(oldHash, 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "device_id", "token_hash", "expires_at",
			"revoked_at", "last_used_at", "impersonated_by", "created_at",
		}).AddRow(
			"01HRCWR7CKMCBEDF2PYQ7G0D2K",
			newTok.UserID, "dev-1", oldHash,
			now.Add(time.Hour), nil, nil, nil, now,
		))

	// UPDATE revoked_at
	mock.ExpectExec(`UPDATE .refresh_tokens. SET .revoked_at.`).
		WithArgs(sqlmock.AnyArg(), "01HRCWR7CKMCBEDF2PYQ7G0D2K").
		WillReturnResult(sqlmock.NewResult(0, 1))

	// INSERT new row
	mock.ExpectExec(`INSERT INTO .refresh_tokens.`).
		WithArgs(
			newTok.ID, newTok.UserID, newTok.DeviceID, newTok.TokenHash,
			newTok.ExpiresAt, nil, nil, nil, sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.Rotate(context.Background(), oldHash, newTok)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshTokenRepository_Rotate_AlreadyRevoked(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewRefreshTokenRepository(gdb)

	revoked := time.Now().UTC().Add(-time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT \* FROM .refresh_tokens. WHERE token_hash = \?.*FOR UPDATE`).
		WithArgs("old-hash", 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "device_id", "token_hash", "expires_at",
			"revoked_at", "last_used_at", "impersonated_by", "created_at",
		}).AddRow(
			"01HRCWR7CKMCBEDF2PYQ7G0D2K", "u", "dev", "old-hash",
			time.Now().UTC().Add(time.Hour), revoked, nil, nil, time.Now().UTC(),
		))
	// No UPDATE / INSERT expected — repo rolls back.
	mock.ExpectRollback()

	err := repo.Rotate(context.Background(), "old-hash", &models.RefreshToken{ID: "x"})
	require.Error(t, err)
	assert.ErrorIs(t, err, repository.ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
