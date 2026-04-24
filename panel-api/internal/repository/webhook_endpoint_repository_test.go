package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestWebhookEndpointRepository_FindByChannelID_NotFound(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewWebhookEndpointRepository(db)

	mock.ExpectQuery("SELECT .* FROM `webhook_endpoints` WHERE channel_id = \\?.*LIMIT").
		WithArgs("nope", 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"channel_id", "last_success_at", "last_error",
			"consecutive_failures", "backoff_until", "updated_at",
		}))

	row, err := repo.FindByChannelID(context.Background(), "nope")
	require.Nil(t, row)
	require.True(t, errors.Is(err, ErrNotFound))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWebhookEndpointRepository_RecordFailure_UpsertsIncrement(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewWebhookEndpointRepository(db)

	backoff := time.Now().Add(5 * time.Minute)

	mock.ExpectExec("INSERT INTO webhook_endpoints.*ON DUPLICATE KEY UPDATE").
		WithArgs("01HF0000000000000000000001", "503 from slack", backoff).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.RecordFailure(context.Background(), "01HF0000000000000000000001", "503 from slack", &backoff)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWebhookEndpointRepository_RecordSuccess_Upsert(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := NewWebhookEndpointRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO `webhook_endpoints`.*ON DUPLICATE KEY UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(time.Now()))
	mock.ExpectCommit()

	err := repo.RecordSuccess(context.Background(), "01HF0000000000000000000001")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
