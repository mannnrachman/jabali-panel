package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// fixedNow keeps time-dependent assertions deterministic across runs.
var magicLinkFixedNow = time.Date(2026, 4, 21, 4, 0, 0, 0, time.UTC)

func newMagicLinkToken() *models.MagicLinkToken {
	return &models.MagicLinkToken{
		ID:                   "01HMAGIC0LINK000000000A",
		ApplicationInstallID: "01HMAGIC0INSTALL00000000",
		PanelUserID:          "01HMAGIC0USER0000000000A",
		TokenHash:            "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		ExpiresAt:            magicLinkFixedNow.Add(60 * time.Second),
		CreatedAt:            magicLinkFixedNow,
	}
}

func TestMagicLinkTokenCreate_Success(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewMagicLinkTokenRepository(db)
	tok := newMagicLinkToken()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO `magic_link_tokens`")).
		WithArgs(tok.ID, tok.ApplicationInstallID, tok.PanelUserID, tok.TokenHash, tok.ExpiresAt, sqlmock.AnyArg(), tok.CreatedAt).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(tok.CreatedAt))
	mock.ExpectCommit()

	require.NoError(t, repo.Create(context.Background(), tok))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMagicLinkTokenCreate_DuplicateTokenHash(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewMagicLinkTokenRepository(db)
	tok := newMagicLinkToken()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO `magic_link_tokens`")).
		WillReturnError(&mysql.MySQLError{Number: 1062, Message: "duplicate token_hash"})
	mock.ExpectRollback()

	err := repo.Create(context.Background(), tok)
	require.ErrorIs(t, err, ErrConflict)
}

func TestMagicLinkTokenFindByTokenHash_Hit(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewMagicLinkTokenRepository(db)
	tok := newMagicLinkToken()

	rows := sqlmock.NewRows([]string{"id", "application_install_id", "panel_user_id", "token_hash", "expires_at", "used_at", "created_at"}).
		AddRow(tok.ID, tok.ApplicationInstallID, tok.PanelUserID, tok.TokenHash, tok.ExpiresAt, nil, tok.CreatedAt)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `magic_link_tokens` WHERE token_hash = ?")).
		WithArgs(tok.TokenHash, 1).
		WillReturnRows(rows)

	got, err := repo.FindByTokenHash(context.Background(), tok.TokenHash)
	require.NoError(t, err)
	require.Equal(t, tok.ID, got.ID)
	require.Nil(t, got.UsedAt)
}

func TestMagicLinkTokenFindByTokenHash_Miss(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewMagicLinkTokenRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `magic_link_tokens` WHERE token_hash = ?")).
		WithArgs("nope", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	got, err := repo.FindByTokenHash(context.Background(), "nope")
	require.Nil(t, got)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestMagicLinkTokenMarkUsed_Success(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewMagicLinkTokenRepository(db)
	tok := newMagicLinkToken()

	rows := sqlmock.NewRows([]string{"id", "application_install_id", "panel_user_id", "token_hash", "expires_at", "used_at", "created_at"}).
		AddRow(tok.ID, tok.ApplicationInstallID, tok.PanelUserID, tok.TokenHash, tok.ExpiresAt, nil, tok.CreatedAt)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT \* FROM .magic_link_tokens. WHERE id = \? .*FOR UPDATE NOWAIT`).
		WithArgs(tok.ID, 1).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE magic_link_tokens SET used_at = NOW(6) WHERE id = ? AND used_at IS NULL")).
		WithArgs(tok.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.MarkUsed(context.Background(), tok.ID))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMagicLinkTokenMarkUsed_AlreadyUsed_RowFlagged(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewMagicLinkTokenRepository(db)
	tok := newMagicLinkToken()
	used := magicLinkFixedNow.Add(-time.Second)

	rows := sqlmock.NewRows([]string{"id", "application_install_id", "panel_user_id", "token_hash", "expires_at", "used_at", "created_at"}).
		AddRow(tok.ID, tok.ApplicationInstallID, tok.PanelUserID, tok.TokenHash, tok.ExpiresAt, used, tok.CreatedAt)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT \* FROM .magic_link_tokens. WHERE id = \? .*FOR UPDATE NOWAIT`).
		WithArgs(tok.ID, 1).
		WillReturnRows(rows)
	mock.ExpectRollback()

	err := repo.MarkUsed(context.Background(), tok.ID)
	require.ErrorIs(t, err, ErrAlreadyUsed)
}

func TestMagicLinkTokenMarkUsed_AlreadyUsed_RaceLost(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewMagicLinkTokenRepository(db)
	tok := newMagicLinkToken()

	rows := sqlmock.NewRows([]string{"id", "application_install_id", "panel_user_id", "token_hash", "expires_at", "used_at", "created_at"}).
		AddRow(tok.ID, tok.ApplicationInstallID, tok.PanelUserID, tok.TokenHash, tok.ExpiresAt, nil, tok.CreatedAt)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT \* FROM .magic_link_tokens. WHERE id = \? .*FOR UPDATE NOWAIT`).
		WithArgs(tok.ID, 1).
		WillReturnRows(rows)
	// Another transaction snuck in between SELECT and UPDATE — UPDATE
	// affects 0 rows because used_at is no longer NULL.
	mock.ExpectExec(regexp.QuoteMeta("UPDATE magic_link_tokens SET used_at = NOW(6) WHERE id = ? AND used_at IS NULL")).
		WithArgs(tok.ID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := repo.MarkUsed(context.Background(), tok.ID)
	require.ErrorIs(t, err, ErrAlreadyUsed)
}

func TestMagicLinkTokenMarkUsed_Locked(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewMagicLinkTokenRepository(db)
	tok := newMagicLinkToken()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT \* FROM .magic_link_tokens. WHERE id = \? .*FOR UPDATE NOWAIT`).
		WithArgs(tok.ID, 1).
		WillReturnError(&mysql.MySQLError{Number: 3572, Message: "lock NOWAIT"})
	mock.ExpectRollback()

	err := repo.MarkUsed(context.Background(), tok.ID)
	require.ErrorIs(t, err, ErrLocked)
}

func TestMagicLinkTokenMarkUsed_NotFound(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewMagicLinkTokenRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT \* FROM .magic_link_tokens. WHERE id = \? .*FOR UPDATE NOWAIT`).
		WithArgs("ghost", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()

	err := repo.MarkUsed(context.Background(), "ghost")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestMagicLinkTokenDeleteExpired(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewMagicLinkTokenRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `magic_link_tokens` WHERE expires_at <= ?")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 7))
	mock.ExpectCommit()

	n, err := repo.DeleteExpired(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 7, n)
	require.NoError(t, mock.ExpectationsWereMet())
}
