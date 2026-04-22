package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func managedIPCols() []string {
	return []string{
		"id", "address", "family", "label",
		"is_default", "is_bound", "is_user_selectable",
		"degraded", "created_at", "updated_at",
	}
}

func TestManagedIPRepository_FindByID_Found(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	now := time.Now()
	mock.ExpectQuery("SELECT .* FROM `managed_ips` WHERE id = \\?.*LIMIT").
		WithArgs(uint64(7), 1).
		WillReturnRows(sqlmock.NewRows(managedIPCols()).AddRow(
			uint64(7), "203.0.113.7", "ipv4", "extra-v4",
			false, true, true, false, now, now,
		))

	row, err := repo.FindByID(context.Background(), 7)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "203.0.113.7", row.Address)
	require.Equal(t, "ipv4", row.Family)
	require.True(t, row.IsBound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_FindByID_NotFound(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	mock.ExpectQuery("SELECT .* FROM `managed_ips` WHERE id = \\?.*LIMIT").
		WithArgs(uint64(99), 1).
		WillReturnRows(sqlmock.NewRows(managedIPCols()))

	row, err := repo.FindByID(context.Background(), 99)
	require.Error(t, err)
	require.Nil(t, row)
	require.True(t, errors.Is(err, ErrNotFound))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_FindByAddress(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	now := time.Now()
	mock.ExpectQuery("SELECT .* FROM `managed_ips` WHERE address = \\?.*LIMIT").
		WithArgs("2001:db8::1", 1).
		WillReturnRows(sqlmock.NewRows(managedIPCols()).AddRow(
			uint64(3), "2001:db8::1", "ipv6", "v6 secondary",
			false, true, false, false, now, now,
		))

	row, err := repo.FindByAddress(context.Background(), "2001:db8::1")
	require.NoError(t, err)
	require.Equal(t, "ipv6", row.Family)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_Create(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	now := time.Now().UTC()
	mock.ExpectBegin()
	// GORM emits MariaDB's RETURNING extension (server >= 10.5) for
	// auto-increment primary keys, so this is a Query, not an Exec —
	// the repo reads the new id+timestamps back into the struct.
	mock.ExpectQuery("INSERT INTO `managed_ips`.*RETURNING `id`,`created_at`,`updated_at`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(uint64(11), now, now))
	mock.ExpectCommit()

	row := &models.ManagedIP{
		Address:   "198.51.100.42",
		Family:    "ipv4",
		Label:     "extra-v4",
		CreatedAt: now,
		UpdatedAt: now,
	}
	err := repo.Create(context.Background(), row)
	require.NoError(t, err)
	require.EqualValues(t, 11, row.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_Create_DuplicateAddress(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	mock.ExpectBegin()
	// Same RETURNING-aware INSERT, but failing with the duplicate-entry
	// stringy error. isDuplicateKey accepts both typed MySQLError and
	// the "Duplicate entry" message form.
	mock.ExpectQuery("INSERT INTO `managed_ips`").
		WillReturnError(errors.New("Error 1062: Duplicate entry '203.0.113.7' for key 'uq_managed_ips_address'"))
	mock.ExpectRollback()

	err := repo.Create(context.Background(), &models.ManagedIP{
		Address:   "203.0.113.7",
		Family:    "ipv4",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrConflict))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_Delete(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `managed_ips` WHERE id = \\?").
		WithArgs(uint64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Delete(context.Background(), 5)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_ListAll(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	now := time.Now()
	mock.ExpectQuery("SELECT .* FROM `managed_ips`.*ORDER BY family asc, is_default desc").
		WillReturnRows(sqlmock.NewRows(managedIPCols()).
			AddRow(uint64(1), "203.0.113.1", "ipv4", "primary", true, false, false, false, now, now).
			AddRow(uint64(2), "203.0.113.2", "ipv4", "secondary", false, true, false, false, now, now).
			AddRow(uint64(3), "2001:db8::1", "ipv6", "primary", true, false, false, false, now, now))

	rows, err := repo.ListAll(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, "203.0.113.1", rows[0].Address)
	require.True(t, rows[0].IsDefault)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_FindUnbound(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	now := time.Now()
	mock.ExpectQuery("SELECT .* FROM `managed_ips` WHERE is_bound = \\?.*ORDER BY id asc").
		WithArgs(false).
		WillReturnRows(sqlmock.NewRows(managedIPCols()).
			AddRow(uint64(4), "203.0.113.99", "ipv4", "pending", false, false, false, false, now, now))

	rows, err := repo.FindUnbound(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.False(t, rows[0].IsBound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_CountDomainsUsingIP(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `domains` WHERE listen_ipv4_id = \\? OR listen_ipv6_id = \\?").
		WithArgs(uint64(2), uint64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(3)))

	got, err := repo.CountDomainsUsingIP(context.Background(), 2)
	require.NoError(t, err)
	require.EqualValues(t, 3, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_FindDefaultByFamily(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	now := time.Now()
	mock.ExpectQuery("SELECT .* FROM `managed_ips` WHERE family = \\? AND is_default = \\?.*LIMIT").
		WithArgs("ipv4", true, 1).
		WillReturnRows(sqlmock.NewRows(managedIPCols()).AddRow(
			uint64(1), "203.0.113.1", "ipv4", "primary",
			true, false, false, false, now, now,
		))

	row, err := repo.FindDefaultByFamily(context.Background(), "ipv4")
	require.NoError(t, err)
	require.True(t, row.IsDefault)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_FindDefaultByFamily_NotFound(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	mock.ExpectQuery("SELECT .* FROM `managed_ips` WHERE family = \\? AND is_default = \\?.*LIMIT").
		WithArgs("ipv6", true, 1).
		WillReturnRows(sqlmock.NewRows(managedIPCols()))

	row, err := repo.FindDefaultByFamily(context.Background(), "ipv6")
	require.Nil(t, row)
	require.True(t, errors.Is(err, ErrNotFound))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_EnsureDefault_EmptyAddressIsNoop(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)
	require.NoError(t, repo.EnsureDefault(context.Background(), "", "ipv4"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_EnsureDefault_InvalidFamily(t *testing.T) {
	db, _, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)
	err := repo.EnsureDefault(context.Background(), "203.0.113.1", "ipvx")
	require.Error(t, err)
}

func TestManagedIPRepository_EnsureDefault_ExistingDefaultIsNoop(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	now := time.Now()
	mock.ExpectQuery("SELECT .* FROM `managed_ips` WHERE family = \\? AND is_default = \\?.*LIMIT").
		WithArgs("ipv4", true, 1).
		WillReturnRows(sqlmock.NewRows(managedIPCols()).AddRow(
			uint64(1), "203.0.113.1", "ipv4", "server primary (v4)",
			true, false, false, false, now, now,
		))
	require.NoError(t, repo.EnsureDefault(context.Background(), "203.0.113.1", "ipv4"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_EnsureDefault_PromotesExistingNonDefault(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	now := time.Now()
	mock.ExpectQuery("SELECT .* FROM `managed_ips` WHERE family = \\? AND is_default = \\?.*LIMIT").
		WithArgs("ipv4", true, 1).
		WillReturnRows(sqlmock.NewRows(managedIPCols()))
	mock.ExpectQuery("SELECT .* FROM `managed_ips` WHERE address = \\?.*LIMIT").
		WithArgs("203.0.113.1", 1).
		WillReturnRows(sqlmock.NewRows(managedIPCols()).AddRow(
			uint64(7), "203.0.113.1", "ipv4", "pre-bound",
			false, true, false, false, now, now,
		))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `managed_ips` SET .*").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.EnsureDefault(context.Background(), "203.0.113.1", "ipv4"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManagedIPRepository_EnsureDefault_InsertsNewRow(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewManagedIPRepository(db)

	now := time.Now().UTC()
	mock.ExpectQuery("SELECT .* FROM `managed_ips` WHERE family = \\? AND is_default = \\?.*LIMIT").
		WithArgs("ipv4", true, 1).
		WillReturnRows(sqlmock.NewRows(managedIPCols()))
	mock.ExpectQuery("SELECT .* FROM `managed_ips` WHERE address = \\?.*LIMIT").
		WithArgs("203.0.113.1", 1).
		WillReturnRows(sqlmock.NewRows(managedIPCols()))
	// GORM on MariaDB uses the RETURNING extension — Create() is a Query,
	// not an Exec (see TestManagedIPRepository_Create for the pattern).
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO `managed_ips`.*RETURNING `id`,`created_at`,`updated_at`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(uint64(42), now, now))
	mock.ExpectCommit()

	require.NoError(t, repo.EnsureDefault(context.Background(), "203.0.113.1", "ipv4"))
	require.NoError(t, mock.ExpectationsWereMet())
}
