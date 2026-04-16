package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func TestServerSettingsRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewServerSettingsRepository(gdb)

	// GORM's First() generates: SELECT ... FROM `server_settings` WHERE ... LIMIT ?
	mock.ExpectQuery(`SELECT .* FROM .server_settings. WHERE .* ORDER BY .* LIMIT`).
		WithArgs(1, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "hostname", "public_ipv4", "public_ipv6", "ns1_name", "ns1_ipv4", "ns2_name", "ns2_ipv4", "admin_email", "updated_at"}))

	_, err := repo.Get(context.Background())
	require.Error(t, err)
	assert.Equal(t, repository.ErrNotFound, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestServerSettingsRepository_Upsert_Create(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewServerSettingsRepository(gdb)
	ctx := context.Background()

	s := &models.ServerSettings{
		Hostname:   "example.com",
		PublicIPv4: "192.0.2.1",
		PublicIPv6: "2001:db8::1",
		NS1Name:    "ns1.example.com",
		NS1IPv4:    "192.0.2.1",
		NS2Name:    "ns2.example.com",
		NS2IPv4:    "192.0.2.2",
		AdminEmail: "admin@example.com",
	}

	// Upsert probes existence first.
	mock.ExpectQuery(`SELECT \* FROM .server_settings. WHERE .server_settings.\..id. = .? ORDER BY .server_settings.\..id. LIMIT .?`).
		WithArgs(1, 1).
		WillReturnError(gorm.ErrRecordNotFound)

	// Row missing → Create inserts. MariaDB dialect uses INSERT ... RETURNING id
	// which GORM issues as a Query, not Exec.
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO .server_settings.`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	err := repo.Upsert(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, uint8(1), s.ID)
	assert.False(t, s.UpdatedAt.IsZero())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestServerSettingsRepository_Upsert_Update(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewServerSettingsRepository(gdb)
	ctx := context.Background()

	s := &models.ServerSettings{
		Hostname:   "newhost.com",
		PublicIPv4: "192.0.2.10",
		NS1Name:    "ns1.newhost.com",
		NS1IPv4:    "192.0.2.10",
	}

	// Upsert probes existence first and finds a row.
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{
		"id", "hostname", "public_ipv4", "public_ipv6",
		"ns1_name", "ns1_ipv4", "ns2_name", "ns2_ipv4",
		"admin_email", "updated_at",
	}).AddRow(1, "old.com", "192.0.2.5", "", "", "", "", "", "", now)
	mock.ExpectQuery(`SELECT \* FROM .server_settings. WHERE .server_settings.\..id. = .? ORDER BY .server_settings.\..id. LIMIT .?`).
		WithArgs(1, 1).
		WillReturnRows(rows)

	// Row exists → Updates issues an UPDATE with all columns via Select("*").Omit("id").
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE .server_settings. SET`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Upsert(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, uint8(1), s.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestServerSettingsRepository_Get_Success(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewServerSettingsRepository(gdb)
	now := time.Now().UTC()

	mock.ExpectQuery(`SELECT .* FROM .server_settings. WHERE .* ORDER BY .* LIMIT`).
		WithArgs(1, 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "hostname", "public_ipv4", "public_ipv6", "ns1_name", "ns1_ipv4", "ns2_name", "ns2_ipv4", "admin_email", "updated_at",
		}).AddRow(
			uint8(1), "example.com", "192.0.2.1", "2001:db8::1", "ns1.example.com", "192.0.2.1", "ns2.example.com", "192.0.2.2", "admin@example.com", now,
		))

	got, err := repo.Get(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint8(1), got.ID)
	assert.Equal(t, "example.com", got.Hostname)
	assert.Equal(t, "192.0.2.1", got.PublicIPv4)
	assert.Equal(t, "2001:db8::1", got.PublicIPv6)
	require.NoError(t, mock.ExpectationsWereMet())
}
