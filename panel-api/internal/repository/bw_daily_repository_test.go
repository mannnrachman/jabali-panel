package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// Reuses newMockBackupDB from backup_job_repository_test.go (same
// package — _test.go helpers are visible across files).

func TestBWDaily_Upsert_StampsTimestamps(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewBWDailyRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `bw_daily`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	row := &models.BWDaily{
		DomainID:      "01J5DOMAIN0000000000000000",
		Day:           time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC),
		BytesTotal:    1234567,
		RequestsTotal: 42,
	}
	require.NoError(t, repo.Upsert(context.Background(), row))
	require.False(t, row.UpdatedAt.IsZero())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBWDaily_SumForDomain_NoRowsReturnsZeros(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewBWDailyRepository(db)

	mock.ExpectQuery("FROM `bw_daily`").
		WillReturnRows(sqlmock.NewRows([]string{"bytes", "reqs"}).AddRow(0, 0))

	from := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	bytes, reqs, err := repo.SumForDomain(context.Background(), "01J5DOMAIN0000000000000000", from, to)
	require.NoError(t, err)
	require.Equal(t, uint64(0), bytes)
	require.Equal(t, uint64(0), reqs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBWDaily_SumForDomain_SumsRows(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewBWDailyRepository(db)

	mock.ExpectQuery("FROM `bw_daily`").
		WillReturnRows(sqlmock.NewRows([]string{"bytes", "reqs"}).AddRow(123456789, 999))

	from := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	bytes, reqs, err := repo.SumForDomain(context.Background(), "01J5DOMAIN0000000000000000", from, time.Time{})
	require.NoError(t, err)
	require.Equal(t, uint64(123456789), bytes)
	require.Equal(t, uint64(999), reqs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBWDaily_SumByDomainIDs_EmptyShortCircuits(t *testing.T) {
	db, _, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewBWDailyRepository(db)

	got, err := repo.SumByDomainIDs(context.Background(), nil, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Empty(t, got)

	got, err = repo.SumByDomainIDs(context.Background(), []string{}, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestBWDaily_SumByDomainIDs_GroupsByDomain(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewBWDailyRepository(db)

	mock.ExpectQuery("FROM `bw_daily`").
		WillReturnRows(sqlmock.NewRows([]string{"domain_id", "bytes"}).
			AddRow("01J5DOMAINAAAAAAAAAAAAAAAA", 1000).
			AddRow("01J5DOMAINBBBBBBBBBBBBBBBB", 2000))

	from := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	got, err := repo.SumByDomainIDs(
		context.Background(),
		[]string{"01J5DOMAINAAAAAAAAAAAAAAAA", "01J5DOMAINBBBBBBBBBBBBBBBB"},
		from,
		time.Time{},
	)
	require.NoError(t, err)
	require.Equal(t, uint64(1000), got["01J5DOMAINAAAAAAAAAAAAAAAA"])
	require.Equal(t, uint64(2000), got["01J5DOMAINBBBBBBBBBBBBBBBB"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBWDaily_SumPerDayForDomain_OrdersAsc(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewBWDailyRepository(db)

	mock.ExpectQuery("FROM `bw_daily`.*ORDER BY day").
		WillReturnRows(sqlmock.NewRows([]string{"day", "bytes_total", "requests_total"}).
			AddRow(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), 100, 1).
			AddRow(time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC), 200, 2).
			AddRow(time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC), 300, 3))

	from := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC)
	got, err := repo.SumPerDayForDomain(context.Background(), "01J5DOMAIN0000000000000000", from, to)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, uint64(100), got[0].BytesTotal)
	require.Equal(t, uint64(300), got[2].BytesTotal)
	require.NoError(t, mock.ExpectationsWereMet())
}
