package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestAuditEventAllForVerify_TotalChainOrder(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewAuditEventRepository(db)

	// Must be ts ASC, id ASC — the order VerifyChain replays the chain in.
	mock.ExpectQuery("SELECT .* FROM `audit_events` ORDER BY ts ASC, id ASC").
		WillReturnRows(sqlmock.NewRows([]string{"id", "ts", "actor_kind", "action", "result"}).
			AddRow("01HZ1", time.Unix(0, 1).UTC(), "system", "a", "ok").
			AddRow("01HZ2", time.Unix(0, 2).UTC(), "admin", "b", "ok"))

	rows, err := repo.AllForVerify(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "01HZ1", rows[0].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuditEventPruneOlderThan_BoundedDeleteReturnsCount(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewAuditEventRepository(db)

	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `audit_events` WHERE ts < ?")).
		WithArgs(cutoff).
		WillReturnResult(sqlmock.NewResult(0, 7))
	mock.ExpectCommit()

	n, err := repo.PruneOlderThan(context.Background(), cutoff)
	require.NoError(t, err)
	require.Equal(t, int64(7), n)
	require.NoError(t, mock.ExpectationsWereMet())
}
