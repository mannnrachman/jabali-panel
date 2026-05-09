package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// UserEgressDropSampleRepository persists per-tick egress drop deltas
// (M34 deep stats — drives the 24h sparkline on the admin Egress
// card). Insert is no-op when delta == 0 so a quiet host doesn't
// accumulate empty rows.
type UserEgressDropSampleRepository interface {
	// Insert writes one sample. Caller-supplied `at` so all samples
	// for the same tick share a timestamp.
	Insert(ctx context.Context, userID string, at time.Time, drops uint64) error

	// ListLast24h returns every sample for one user in the last 24h
	// ordered ascending. Caller groups into hourly buckets at render
	// time.
	ListLast24h(ctx context.Context, userID string, now time.Time) ([]models.UserEgressDropSample, error)

	// PruneOlderThan deletes every sample with at < cutoff.
	// Reconciler runs this once per tick with cutoff = now - 25h
	// (1h buffer past the rendering window).
	PruneOlderThan(ctx context.Context, cutoff time.Time) error
}

type userEgressDropSampleRepo struct{ db *gorm.DB }

func NewUserEgressDropSampleRepository(db *gorm.DB) UserEgressDropSampleRepository {
	return &userEgressDropSampleRepo{db: db}
}

func (r *userEgressDropSampleRepo) Insert(ctx context.Context, userID string, at time.Time, drops uint64) error {
	if userID == "" || drops == 0 {
		return nil
	}
	row := &models.UserEgressDropSample{
		UserID: userID,
		At:     at.UTC(),
		Drops:  drops,
	}
	return r.db.WithContext(ctx).Create(row).Error
}

func (r *userEgressDropSampleRepo) ListLast24h(ctx context.Context, userID string, now time.Time) ([]models.UserEgressDropSample, error) {
	var rows []models.UserEgressDropSample
	cutoff := now.Add(-24 * time.Hour)
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND at >= ?", userID, cutoff).
		Order("at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *userEgressDropSampleRepo) PruneOlderThan(ctx context.Context, cutoff time.Time) error {
	return r.db.WithContext(ctx).
		Where("at < ?", cutoff).
		Delete(&models.UserEgressDropSample{}).Error
}
