package repository_test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// TestPhpMyAdminSSOTokenRepository_Create verifies token insertion.
func TestPhpMyAdminSSOTokenRepository_Create(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewPhpMyAdminSSOTokenRepository(db)

	expiresAt := time.Now().Add(5 * time.Minute)

	token := &models.PhpMyAdminSSOToken{
		ID:         "sso_token_abc123",
		UserID:     "user_xyz",
		DatabaseID: "db_001",
		TokenHash:  "abc123def456789",
		ExpiresAt:  expiresAt,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO `phpmyadmin_sso_tokens` (`id`,`user_id`,`database_id`,`token_hash`,`expires_at`) VALUES (?,?,?,?,?) RETURNING `created_at`")).
		WithArgs(
			token.ID,
			token.UserID,
			token.DatabaseID,
			token.TokenHash,
			sqlmock.AnyArg(), // ExpiresAt is a time.Time, hard to match exactly
		).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(time.Now()))
	mock.ExpectCommit()

	err := repo.Create(context.Background(), token)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPhpMyAdminSSOTokenRepository_ConsumeByHash_Happy verifies successful consume
// (select + delete in one transaction).
func TestPhpMyAdminSSOTokenRepository_ConsumeByHash_Happy(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewPhpMyAdminSSOTokenRepository(db)

	tokenHash := "abc123def456789"
	expiresAt := time.Now().Add(5 * time.Minute)

	mock.ExpectBegin()
	// GORM adds ORDER BY and LIMIT to SELECT queries. Match the full pattern.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `phpmyadmin_sso_tokens` WHERE token_hash = ? AND expires_at > ? ORDER BY `phpmyadmin_sso_tokens`.`id` LIMIT ? FOR UPDATE")).
		WithArgs(tokenHash, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "user_id", "database_id", "token_hash", "expires_at", "created_at"},
		).AddRow("sso_token_abc123", "user_xyz", "db_001", tokenHash, expiresAt, time.Now()))

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `phpmyadmin_sso_tokens` WHERE `phpmyadmin_sso_tokens`.`id` = ?")).
		WithArgs("sso_token_abc123").
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectCommit()

	token, err := repo.ConsumeByHash(context.Background(), tokenHash)
	require.NoError(t, err)
	require.NotNil(t, token)
	require.Equal(t, "sso_token_abc123", token.ID)
	require.Equal(t, "user_xyz", token.UserID)
	require.Equal(t, "db_001", token.DatabaseID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPhpMyAdminSSOTokenRepository_ConsumeByHash_Expired verifies that expired tokens
// return ErrNotFound (no delete occurs).
func TestPhpMyAdminSSOTokenRepository_ConsumeByHash_Expired(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewPhpMyAdminSSOTokenRepository(db)

	tokenHash := "expired_token_hash"

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `phpmyadmin_sso_tokens` WHERE token_hash = ? AND expires_at > ? ORDER BY `phpmyadmin_sso_tokens`.`id` LIMIT ? FOR UPDATE")).
		WithArgs(tokenHash, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "user_id", "database_id", "token_hash", "expires_at", "created_at"},
		))
	mock.ExpectRollback()

	token, err := repo.ConsumeByHash(context.Background(), tokenHash)
	require.Error(t, err)
	require.Nil(t, token)
	require.Equal(t, repository.ErrNotFound, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPhpMyAdminSSOTokenRepository_ConsumeByHash_Unknown verifies that unknown tokens
// return ErrNotFound.
func TestPhpMyAdminSSOTokenRepository_ConsumeByHash_Unknown(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewPhpMyAdminSSOTokenRepository(db)

	tokenHash := "nonexistent_hash"

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `phpmyadmin_sso_tokens` WHERE token_hash = ? AND expires_at > ? ORDER BY `phpmyadmin_sso_tokens`.`id` LIMIT ? FOR UPDATE")).
		WithArgs(tokenHash, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "user_id", "database_id", "token_hash", "expires_at", "created_at"},
		))
	mock.ExpectRollback()

	token, err := repo.ConsumeByHash(context.Background(), tokenHash)
	require.Error(t, err)
	require.Nil(t, token)
	require.Equal(t, repository.ErrNotFound, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPhpMyAdminSSOTokenRepository_ConsumeByHash_Twice verifies that a second consume
// on the same token returns ErrNotFound (token already deleted).
func TestPhpMyAdminSSOTokenRepository_ConsumeByHash_Twice(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewPhpMyAdminSSOTokenRepository(db)

	tokenHash := "single_use_token"
	expiresAt := time.Now().Add(5 * time.Minute)

	// First consume: succeeds
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `phpmyadmin_sso_tokens` WHERE token_hash = ? AND expires_at > ? ORDER BY `phpmyadmin_sso_tokens`.`id` LIMIT ? FOR UPDATE")).
		WithArgs(tokenHash, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "user_id", "database_id", "token_hash", "expires_at", "created_at"},
		).AddRow("sso_token_abc123", "user_xyz", "db_001", tokenHash, expiresAt, time.Now()))

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `phpmyadmin_sso_tokens` WHERE `phpmyadmin_sso_tokens`.`id` = ?")).
		WithArgs("sso_token_abc123").
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectCommit()

	token, err := repo.ConsumeByHash(context.Background(), tokenHash)
	require.NoError(t, err)
	require.NotNil(t, token)

	// Second consume: returns ErrNotFound (no rows found)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `phpmyadmin_sso_tokens` WHERE token_hash = ? AND expires_at > ? ORDER BY `phpmyadmin_sso_tokens`.`id` LIMIT ? FOR UPDATE")).
		WithArgs(tokenHash, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "user_id", "database_id", "token_hash", "expires_at", "created_at"},
		))
	mock.ExpectRollback()

	token2, err := repo.ConsumeByHash(context.Background(), tokenHash)
	require.Error(t, err)
	require.Nil(t, token2)
	require.Equal(t, repository.ErrNotFound, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPhpMyAdminSSOTokenRepository_PurgeExpired verifies expired token deletion
// and returns the correct count.
func TestPhpMyAdminSSOTokenRepository_PurgeExpired(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewPhpMyAdminSSOTokenRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `phpmyadmin_sso_tokens` WHERE expires_at <= ?")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	count, err := repo.PurgeExpired(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(3), count)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPhpMyAdminSSOTokenRepository_PurgeExpired_None verifies the happy path
// when there are no expired tokens.
func TestPhpMyAdminSSOTokenRepository_PurgeExpired_None(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewPhpMyAdminSSOTokenRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `phpmyadmin_sso_tokens` WHERE expires_at <= ?")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	count, err := repo.PurgeExpired(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(0), count)
	require.NoError(t, mock.ExpectationsWereMet())
}
