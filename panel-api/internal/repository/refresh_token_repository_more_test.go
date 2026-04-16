package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func TestRefreshTokenRepository_FindByHash_Found(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewRefreshTokenRepository(gdb)
	now := time.Now().UTC()

	mock.ExpectQuery(`SELECT \* FROM .refresh_tokens. WHERE token_hash = \?`).
		WithArgs("abc", 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "device_id", "token_hash", "expires_at",
			"revoked_at", "last_used_at", "impersonated_by", "created_at",
		}).AddRow(
			"01HRCWR7CKMCBEDF2PYQ7G0D2K", "u1", "dev", "abc",
			now.Add(time.Hour), nil, nil, nil, now,
		))

	tok, err := repo.FindByHash(context.Background(), "abc")
	require.NoError(t, err)
	assert.Equal(t, "u1", tok.UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshTokenRepository_FindByHash_NotFound(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewRefreshTokenRepository(gdb)

	mock.ExpectQuery(`SELECT \* FROM .refresh_tokens. WHERE token_hash = \?`).
		WithArgs("missing", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, err := repo.FindByHash(context.Background(), "missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, repository.ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshTokenRepository_Revoke(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewRefreshTokenRepository(gdb)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE .refresh_tokens. SET .revoked_at.`).
		WithArgs(sqlmock.AnyArg(), "01HRCWR7CKMCBEDF2PYQ7G0D2K").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Revoke(context.Background(), "01HRCWR7CKMCBEDF2PYQ7G0D2K", time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshTokenRepository_Revoke_NotFound(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewRefreshTokenRepository(gdb)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE .refresh_tokens. SET .revoked_at.`).
		WithArgs(sqlmock.AnyArg(), "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := repo.Revoke(context.Background(), "missing", time.Now().UTC())
	require.Error(t, err)
	assert.ErrorIs(t, err, repository.ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshTokenRepository_RevokeAllForUser(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewRefreshTokenRepository(gdb)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE .refresh_tokens. SET .revoked_at.`).
		WithArgs(sqlmock.AnyArg(), "user-1").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	err := repo.RevokeAllForUser(context.Background(), "user-1", time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
