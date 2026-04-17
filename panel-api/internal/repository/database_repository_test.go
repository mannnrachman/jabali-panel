package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()

	raw, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)

	// MariaDB version probe GORM sends on open.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).
			AddRow("10.11.0-MariaDB"))

	db, err := gorm.Open(
		mysql.New(mysql.Config{Conn: raw}),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	require.NoError(t, err)

	return db, mock, raw
}


// TestCountByUserID verifies correct count for user's databases
func TestCountByUserID(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewDatabaseRepository(db)

	// Expect COUNT query
	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `databases` WHERE user_id = ?")).
		WithArgs("user1").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(3))

	count, err := repo.CountByUserID(context.Background(), "user1")
	require.NoError(t, err)
	require.Equal(t, int64(3), count)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFindByID_Found verifies database retrieval
func TestFindByID_Found(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewDatabaseRepository(db)

	// Expect SELECT query (GORM adds ORDER BY and LIMIT for First())
	mock.ExpectQuery("SELECT .* FROM `databases` WHERE id = \\?.*LIMIT").
		WithArgs("db_abc123", 1).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "user_id", "name", "engine", "charset", "collation", "created_at", "updated_at"},
		).AddRow("db_abc123", "user1", "testdb", "mariadb", "utf8mb4", "utf8mb4_unicode_ci", time.Now(), time.Now()))

	d, err := repo.FindByID(context.Background(), "db_abc123")
	require.NoError(t, err)
	require.NotNil(t, d)
	require.Equal(t, "db_abc123", d.ID)
	require.Equal(t, "user1", d.UserID)
	require.Equal(t, "testdb", d.Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFindByID_NotFound verifies ErrNotFound when database doesn't exist
func TestFindByID_NotFound(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewDatabaseRepository(db)

	// Expect SELECT query returning no rows (GORM adds ORDER BY and LIMIT for First())
	mock.ExpectQuery("SELECT .* FROM `databases` WHERE id = \\?.*LIMIT").
		WithArgs("db_nonexistent", 1).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "user_id", "name", "engine", "charset", "collation", "created_at", "updated_at"},
		))

	d, err := repo.FindByID(context.Background(), "db_nonexistent")
	require.Error(t, err)
	require.Nil(t, d)
	require.Equal(t, ErrNotFound, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestListByUserID verifies database listing for a specific user
func TestListByUserID(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewDatabaseRepository(db)

	// Expect COUNT query
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `databases` WHERE user_id = \\?").
		WithArgs("user1").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(2))

	// Expect SELECT query with ORDER BY and LIMIT (added by applyListOptions)
	mock.ExpectQuery("SELECT .* FROM `databases` WHERE user_id = \\?.*LIMIT").
		WithArgs("user1", 20).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "user_id", "name", "engine", "charset", "collation", "created_at", "updated_at"},
		).
			AddRow("db_abc123", "user1", "testdb1", "mariadb", "utf8mb4", "utf8mb4_unicode_ci", time.Now(), time.Now()).
			AddRow("db_xyz789", "user1", "testdb2", "mariadb", "utf8mb4", "utf8mb4_unicode_ci", time.Now(), time.Now()))

	databases, total, err := repo.ListByUserID(context.Background(), "user1", ListOptions{Limit: 20})
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, databases, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}
