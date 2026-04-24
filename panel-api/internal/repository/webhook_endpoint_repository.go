package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// WebhookEndpointRepository covers the webhook_endpoints sidecar from
// migration 000064. One row per NotificationChannel. Row is created on
// demand (RecordSuccess / RecordFailure are both upserts).
type WebhookEndpointRepository interface {
	FindByChannelID(ctx context.Context, channelID string) (*models.WebhookEndpoint, error)

	// RecordSuccess resets consecutive_failures to 0 and stamps
	// last_success_at=now. Upsert: creates the row if missing.
	RecordSuccess(ctx context.Context, channelID string) error

	// RecordFailure increments consecutive_failures, sets last_error,
	// and optionally schedules a backoff_until. Upsert.
	RecordFailure(ctx context.Context, channelID, errMsg string, backoffUntil *time.Time) error

	Delete(ctx context.Context, channelID string) error
}

type webhookEndpointRepo struct{ db *gorm.DB }

func NewWebhookEndpointRepository(db *gorm.DB) WebhookEndpointRepository {
	return &webhookEndpointRepo{db: db}
}

func (r *webhookEndpointRepo) FindByChannelID(ctx context.Context, channelID string) (*models.WebhookEndpoint, error) {
	var row models.WebhookEndpoint
	if err := r.db.WithContext(ctx).Where("channel_id = ?", channelID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (r *webhookEndpointRepo) RecordSuccess(ctx context.Context, channelID string) error {
	now := time.Now().UTC()
	row := models.WebhookEndpoint{
		ChannelID:           channelID,
		LastSuccessAt:       &now,
		ConsecutiveFailures: 0,
		LastError:           "",
		BackoffUntil:        nil,
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "channel_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"last_success_at", "consecutive_failures", "last_error", "backoff_until",
			}),
		}).
		Create(&row).Error
}

func (r *webhookEndpointRepo) RecordFailure(ctx context.Context, channelID, errMsg string, backoffUntil *time.Time) error {
	// Use a write-side SQL UPDATE for the increment so we don't need a
	// read-modify-write round trip; INSERT via upsert when missing.
	err := r.db.WithContext(ctx).
		Exec(`
			INSERT INTO webhook_endpoints
			  (channel_id, consecutive_failures, last_error, backoff_until)
			VALUES
			  (?, 1, ?, ?)
			ON DUPLICATE KEY UPDATE
			  consecutive_failures = consecutive_failures + 1,
			  last_error = VALUES(last_error),
			  backoff_until = VALUES(backoff_until)
		`, channelID, errMsg, backoffUntil).Error
	return err
}

func (r *webhookEndpointRepo) Delete(ctx context.Context, channelID string) error {
	res := r.db.WithContext(ctx).Delete(&models.WebhookEndpoint{}, "channel_id = ?", channelID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
