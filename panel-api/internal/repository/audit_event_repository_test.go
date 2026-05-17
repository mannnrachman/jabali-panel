package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// strptr is a local helper for the *string audit columns.
func strptr(s string) *string { return &s }

func TestAuditEventCreate_Success(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewAuditEventRepository(db)
	e := &models.AuditEvent{
		ID:         "01HZAUDIT0000000000000001A",
		TS:         time.Now(),
		ActorKind:  models.AuditActorAdmin,
		Action:     "POST /api/v1/admin/automation/tokens",
		TargetType: "token",
		TargetID:   "01HZTOK00000000000000001AA",
		Result:     models.AuditResultOK,
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `audit_events`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Create(context.Background(), e))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuditEventFindByID_NotFound(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewAuditEventRepository(db)

	mock.ExpectQuery("SELECT .* FROM `audit_events` WHERE id = \\?.*LIMIT").
		WithArgs("missing", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"})) // no rows -> ErrRecordNotFound

	_, err := repo.FindByID(context.Background(), "missing")
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ListBySubject MUST scope by subject_user_id server-side (IDOR scar):
// assert the SQL carries the WHERE subject_user_id filter on both the
// count and the find.
func TestAuditEventListBySubject_ScopesBySubject(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewAuditEventRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `audit_events` WHERE subject_user_id = ?")).
		WithArgs("user1").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(2))

	mock.ExpectQuery("SELECT .* FROM `audit_events` WHERE subject_user_id = \\?").
		WithArgs("user1", 20).
		WillReturnRows(sqlmock.NewRows([]string{"id", "ts", "actor_kind", "action", "target_type", "target_id", "result"}).
			AddRow("01HZAUDIT0000000000000010A", time.Now(), models.AuditActorUser, "POST /api/v1/files/upload", "file", "/x", models.AuditResultOK).
			AddRow("01HZAUDIT0000000000000011A", time.Now(), models.AuditActorAdmin, "POST /api/v1/admin/users/:id/2fa/reset", "user", "user1", models.AuditResultOK))

	rows, total, err := repo.ListBySubject(context.Background(), "user1", ListOptions{Limit: 20})
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, rows, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

// SetHashes back-fills a fallback row's hashes ONLY while row_hash is
// NULL — RowsAffected==0 means already sealed / not found and must
// surface as ErrNotFound (never overwrite a sealed row).
func TestAuditEventSetHashes_GatedOnNullRowHash(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewAuditEventRepository(db)

	// Sealed/absent row -> 0 rows affected -> ErrNotFound.
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `audit_events` SET").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "sealed").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	require.ErrorIs(t,
		repo.SetHashes(context.Background(), "sealed", "prevH", "rowH"),
		ErrNotFound)

	// Fallback row still NULL -> 1 affected -> nil.
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `audit_events` SET").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "fallback").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t,
		repo.SetHashes(context.Background(), "fallback", "prevH", "rowH"))

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuditEventLatestRowHash_GenesisEmpty(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewAuditEventRepository(db)

	mock.ExpectQuery("SELECT .* FROM `audit_events` WHERE row_hash IS NOT NULL.*ORDER BY ts DESC").
		WillReturnRows(sqlmock.NewRows([]string{"id"})) // none sealed yet

	h, err := repo.LatestRowHash(context.Background())
	require.NoError(t, err)
	require.Equal(t, "", h)
	require.NoError(t, mock.ExpectationsWereMet())

	// And a sealed row returns its hash.
	db2, mock2, raw2 := newMockDB(t)
	defer raw2.Close()
	repo2 := NewAuditEventRepository(db2)
	mock2.ExpectQuery("SELECT .* FROM `audit_events` WHERE row_hash IS NOT NULL.*ORDER BY ts DESC").
		WillReturnRows(sqlmock.NewRows([]string{"id", "row_hash"}).AddRow("01HZAUDIT0000000000000099A", strptr("deadbeef")))
	h2, err := repo2.LatestRowHash(context.Background())
	require.NoError(t, err)
	require.Equal(t, "deadbeef", h2)
	require.NoError(t, mock2.ExpectationsWereMet())
}
