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

func webPushSubscriptionCols() []string {
	return []string{
		"id", "user_id", "endpoint", "p256dh", "auth",
		"user_agent", "created_at", "last_used_at",
	}
}

func TestWebPushSubscriptionRepository_Upsert(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewWebPushSubscriptionRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO `webpush_subscriptions`.*ON DUPLICATE KEY UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(time.Now()))
	mock.ExpectCommit()

	err := repo.Upsert(context.Background(), &models.WebPushSubscription{
		ID:       "01HF000SUB0000000000000001",
		UserID:   "01HF000USER0000000000000",
		Endpoint: "https://fcm.googleapis.com/fcm/send/abc",
		P256dh:   "BK_pubkey_placeholder",
		Auth:     "secret-placeholder",
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWebPushSubscriptionRepository_FindByUser(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewWebPushSubscriptionRepository(db)

	now := time.Now()
	mock.ExpectQuery("SELECT .* FROM `webpush_subscriptions` WHERE user_id = \\?.*ORDER BY created_at DESC").
		WithArgs("01HF000USER0000000000000").
		WillReturnRows(sqlmock.NewRows(webPushSubscriptionCols()).
			AddRow("01HF000SUB0000000000000001", "01HF000USER0000000000000",
				"https://fcm.googleapis.com/fcm/send/abc", "pub", "auth",
				"Mozilla/5.0", now, nil).
			AddRow("01HF000SUB0000000000000002", "01HF000USER0000000000000",
				"https://updates.push.services.mozilla.com/wpush/v2/xyz", "pub", "auth",
				"Mozilla/5.0 (Firefox)", now, nil))

	rows, err := repo.FindByUser(context.Background(), "01HF000USER0000000000000")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWebPushSubscriptionRepository_DeleteByEndpoint_Gone(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewWebPushSubscriptionRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `webpush_subscriptions` WHERE endpoint = \\?").
		WithArgs("https://gone.example/endpoint").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.DeleteByEndpoint(context.Background(), "https://gone.example/endpoint")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWebPushSubscriptionRepository_DeleteByID_NotFound(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewWebPushSubscriptionRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `webpush_subscriptions` WHERE id = \\?").
		WithArgs("missing").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := repo.DeleteByID(context.Background(), "missing")
	require.True(t, errors.Is(err, ErrNotFound))
	require.NoError(t, mock.ExpectationsWereMet())
}
