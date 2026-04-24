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

func notificationChannelCols() []string {
	return []string{
		"id", "name", "kind", "config_json", "enabled", "created_at", "updated_at",
	}
}

func TestNotificationChannelRepository_FindByID_Found(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewNotificationChannelRepository(db)

	now := time.Now()
	mock.ExpectQuery("SELECT .* FROM `notification_channels` WHERE id = \\?.*LIMIT").
		WithArgs("01HF0000000000000000000001", 1).
		WillReturnRows(sqlmock.NewRows(notificationChannelCols()).AddRow(
			"01HF0000000000000000000001", "Ops Slack", "slack",
			[]byte(`{"url":"https://hooks.slack.com/x"}`),
			true, now, now,
		))

	row, err := repo.FindByID(context.Background(), "01HF0000000000000000000001")
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "slack", row.Kind)
	require.Equal(t, "https://hooks.slack.com/x", row.Config.URL)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNotificationChannelRepository_FindByID_NotFound(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewNotificationChannelRepository(db)

	mock.ExpectQuery("SELECT .* FROM `notification_channels` WHERE id = \\?.*LIMIT").
		WithArgs("missing", 1).
		WillReturnRows(sqlmock.NewRows(notificationChannelCols()))

	row, err := repo.FindByID(context.Background(), "missing")
	require.Nil(t, row)
	require.True(t, errors.Is(err, ErrNotFound))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNotificationChannelRepository_FindEnabledByKind(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewNotificationChannelRepository(db)

	now := time.Now()
	mock.ExpectQuery("SELECT .* FROM `notification_channels` WHERE kind = \\? AND enabled = \\?.*ORDER BY id asc").
		WithArgs("slack", true).
		WillReturnRows(sqlmock.NewRows(notificationChannelCols()).
			AddRow("01HF0000000000000000000001", "Ops Slack", "slack",
				[]byte(`{"url":"https://hooks.slack.com/x"}`), true, now, now).
			AddRow("01HF0000000000000000000002", "Dev Slack", "slack",
				[]byte(`{"url":"https://hooks.slack.com/y"}`), true, now, now))

	rows, err := repo.FindEnabledByKind(context.Background(), "slack")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNotificationChannelRepository_Delete_NotFound(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewNotificationChannelRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `notification_channels` WHERE id = \\?").
		WithArgs("nope").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := repo.Delete(context.Background(), "nope")
	require.True(t, errors.Is(err, ErrNotFound))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNotificationChannelRepository_ConfigRoundTrip(t *testing.T) {
	// Unit-level check that the NotificationChannelConfig Scan/Value
	// helpers round-trip the JSON without losing fields. Uses the
	// sqlmock to simulate the DB returning raw JSON bytes.
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewNotificationChannelRepository(db)

	now := time.Now()
	mock.ExpectQuery("SELECT .* FROM `notification_channels` WHERE id = \\?.*LIMIT").
		WithArgs("01HF0000000000000000000009", 1).
		WillReturnRows(sqlmock.NewRows(notificationChannelCols()).AddRow(
			"01HF0000000000000000000009", "ntfy-public", "ntfy",
			[]byte(`{"url":"https://ntfy.sh/alerts","priority":4,"tags":["warning","panel"]}`),
			true, now, now,
		))

	row, err := repo.FindByID(context.Background(), "01HF0000000000000000000009")
	require.NoError(t, err)
	require.Equal(t, "https://ntfy.sh/alerts", row.Config.URL)
	require.Equal(t, 4, row.Config.Priority)
	require.Equal(t, []string{"warning", "panel"}, row.Config.Tags)

	// And serialise back out; the Value() method must produce valid JSON.
	val, err := row.Config.Value()
	require.NoError(t, err)
	require.NotEmpty(t, val)
	_, ok := val.([]byte)
	require.True(t, ok)

	require.NoError(t, mock.ExpectationsWereMet())
}

var _ = models.NotificationChannel{} // keep the models import honest even if all tests are query-level
