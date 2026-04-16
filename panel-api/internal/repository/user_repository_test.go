package repository_test

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// newMockDB wires sqlmock to GORM so unit tests can assert on the exact
// SQL the repository issues without needing a live MariaDB.
func newMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()

	raw, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)

	// MariaDB version probe GORM sends on open.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("10.11.6-MariaDB"))

	gdb, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      raw,
		SkipInitializeWithVersion: false,
	}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	return gdb, mock, raw
}

func TestUserRepository_Create(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewUserRepository(gdb)

	u := &models.User{
		ID:           "01HRCWR7CKMCBEDF2PYQ7G0D2J",
		Email:        "alice@example.com",
		PasswordHash: "$2a$12$abcdefghijklmnopqrstu",
		IsAdmin:      false,
	}

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .users.`).
		WithArgs(
			u.ID,
			u.Email,
			nil,              // username
			"", // name_first default
			"", // name_last default
			u.PasswordHash,
			false,            // is_admin
			nil,              // package_id
			nil,              // linux_uid
			sqlmock.AnyArg(), // created_at
			sqlmock.AnyArg(), // updated_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.Create(context.Background(), u)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepository_FindByEmail_Found(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewUserRepository(gdb)

	now := time.Now().UTC()
	mock.ExpectQuery(`SELECT \* FROM .users. WHERE email = \? ORDER BY .users.\..id. LIMIT \?`).
		WithArgs("alice@example.com", 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "email", "name_first", "name_last", "password_hash",
			"is_admin", "linux_uid", "created_at", "updated_at",
		}).AddRow(
			"01HRCWR7CKMCBEDF2PYQ7G0D2J", "alice@example.com", "", "",
			"$2a$12$hash", false, nil, now, now,
		))

	got, err := repo.FindByEmail(context.Background(), "alice@example.com")
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", got.Email)
	assert.Equal(t, "01HRCWR7CKMCBEDF2PYQ7G0D2J", got.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepository_FindByEmail_NotFound(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewUserRepository(gdb)

	mock.ExpectQuery(`SELECT \* FROM .users. WHERE email = \?`).
		WithArgs("nobody@example.com", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"})) // no rows

	_, err := repo.FindByEmail(context.Background(), "nobody@example.com")
	require.Error(t, err)
	assert.ErrorIs(t, err, repository.ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
