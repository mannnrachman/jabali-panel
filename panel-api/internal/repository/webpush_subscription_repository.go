package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// WebPushSubscriptionRepository covers webpush_subscriptions from
// migration 000064. See ADR-0057: one row per browser enrolled by an
// admin; endpoint is UNIQUE so re-enrolling the same browser updates
// rather than duplicates.
type WebPushSubscriptionRepository interface {
	// Upsert inserts a new subscription or refreshes the p256dh/auth of
	// an existing one (same endpoint URL). Used by the enrolment
	// endpoint; the browser handing us the same endpoint with rotated
	// keys is a legitimate case the Push API permits.
	Upsert(ctx context.Context, sub *models.WebPushSubscription) error

	FindByID(ctx context.Context, id string) (*models.WebPushSubscription, error)
	FindByUser(ctx context.Context, userID string) ([]models.WebPushSubscription, error)
	FindByEndpoint(ctx context.Context, endpoint string) (*models.WebPushSubscription, error)

	// FindAll returns every enrolled subscription. The webpush sender's
	// broadcast path uses it for envelopes with no UserID (system-wide
	// events like ssh.login or disk.full) — every admin who's opted in
	// to push should hear about them. The set is small (one row per
	// enrolled browser), so a full scan is cheaper than a per-admin
	// fan-out loop.
	FindAll(ctx context.Context) ([]models.WebPushSubscription, error)

	// DeleteByEndpoint is called by the webpush sender when the browser
	// push service responds 410 Gone. The endpoint URL is the globally
	// UNIQUE key the dispatcher has in hand, so routing deletion via it
	// saves a round-trip compared to looking up by id first.
	DeleteByEndpoint(ctx context.Context, endpoint string) error

	// DeleteByID removes a single subscription by its primary key.
	// Used by the UI "disable push on this browser" action.
	DeleteByID(ctx context.Context, id string) error

	// TouchLastUsed stamps last_used_at=now. Called on every successful
	// push delivery so the UI can show "last used N min ago" per
	// browser.
	TouchLastUsed(ctx context.Context, id string) error
}

type webPushSubscriptionRepo struct{ db *gorm.DB }

func NewWebPushSubscriptionRepository(db *gorm.DB) WebPushSubscriptionRepository {
	return &webPushSubscriptionRepo{db: db}
}

func (r *webPushSubscriptionRepo) Upsert(ctx context.Context, sub *models.WebPushSubscription) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "endpoint"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"user_id", "p256dh", "auth", "user_agent", "last_used_at",
			}),
		}).
		Create(sub).Error
}

func (r *webPushSubscriptionRepo) FindByID(ctx context.Context, id string) (*models.WebPushSubscription, error) {
	var row models.WebPushSubscription
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (r *webPushSubscriptionRepo) FindByUser(ctx context.Context, userID string) ([]models.WebPushSubscription, error) {
	var rows []models.WebPushSubscription
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&rows).Error
	return rows, err
}

func (r *webPushSubscriptionRepo) FindAll(ctx context.Context) ([]models.WebPushSubscription, error) {
	var rows []models.WebPushSubscription
	err := r.db.WithContext(ctx).
		Order("created_at DESC").
		Find(&rows).Error
	return rows, err
}

func (r *webPushSubscriptionRepo) FindByEndpoint(ctx context.Context, endpoint string) (*models.WebPushSubscription, error) {
	var row models.WebPushSubscription
	if err := r.db.WithContext(ctx).Where("endpoint = ?", endpoint).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (r *webPushSubscriptionRepo) DeleteByEndpoint(ctx context.Context, endpoint string) error {
	res := r.db.WithContext(ctx).Delete(&models.WebPushSubscription{}, "endpoint = ?", endpoint)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *webPushSubscriptionRepo) DeleteByID(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Delete(&models.WebPushSubscription{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *webPushSubscriptionRepo) TouchLastUsed(ctx context.Context, id string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&models.WebPushSubscription{}).
		Where("id = ?", id).
		Update("last_used_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
