package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func notificationHistoryCols() []string {
	return []string{
		"id", "channel_id", "event_kind", "severity", "title", "body",
		"deeplink", "outcome", "retry_count", "error_message", "read_at",
		"user_id", "created_at", "updated_at",
	}
}

func TestNotificationHistoryRepository_Create(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewNotificationHistoryRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO `notification_history`").
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(time.Now(), time.Now()))
	mock.ExpectCommit()

	err := repo.Create(context.Background(), &models.NotificationHistory{
		ID:        "01HF0000000000000000000001",
		EventKind: "domain.expiry.7d",
		Severity:  models.NotificationSeverityWarning,
		Title:     "example.com expires in 7d",
		Body:      "Renew the domain.",
		Outcome:   models.NotificationOutcomePending,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNotificationHistoryRepository_UpdateOutcome(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewNotificationHistoryRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `notification_history` SET.*WHERE id = \\?").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.UpdateOutcome(context.Background(), "01HF0000000000000000000001", models.NotificationOutcomeSent, "", 0)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNotificationHistoryRepository_CountUnreadForUser(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewNotificationHistoryRepository(db)

	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `notification_history` WHERE channel_id IS NULL AND \\(user_id = \\? AND read_at IS NULL\\)").
		WithArgs("01HF000USER0000000000000").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(7))

	n, err := repo.CountUnreadForUser(context.Background(), "01HF000USER0000000000000")
	require.NoError(t, err)
	require.Equal(t, int64(7), n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNotificationHistoryRepository_MarkAllReadForUser(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewNotificationHistoryRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `notification_history` SET.*WHERE user_id = \\? AND channel_id IS NULL AND read_at IS NULL").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	n, err := repo.MarkAllReadForUser(context.Background(), "01HF000USER0000000000000")
	require.NoError(t, err)
	require.Equal(t, int64(3), n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNotificationHistoryRepository_ListRecentByEvent(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewNotificationHistoryRepository(db)

	since := time.Now().Add(-10 * time.Minute)
	now := time.Now()

	mock.ExpectQuery("SELECT .* FROM `notification_history` WHERE event_kind = \\? AND created_at >= \\?").
		WithArgs("disk.full.95", since).
		WillReturnRows(sqlmock.NewRows(notificationHistoryCols()).
			AddRow("01HF0000000000000000000001", nil, "disk.full.95", "critical",
				"Disk almost full", "95% on /", "", "sent", 0, "", nil, nil, now, now))

	rows, err := repo.ListRecentByEvent(context.Background(), "disk.full.95", since)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "disk.full.95", rows[0].EventKind)
	require.NoError(t, mock.ExpectationsWereMet())
}
