package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// LogAccessStreamRepository defines data access for log access streams.
type LogAccessStreamRepository interface {
	Create(ctx context.Context, stream *models.LogAccessStream) error
	FindByStreamKey(ctx context.Context, streamKey string) (*models.LogAccessStream, error)
	DeleteByID(ctx context.Context, id string) error
	CleanupExpired(ctx context.Context) (int64, error)
	CountByUserID(ctx context.Context, userID string) (int64, error)
}

type logAccessStreamRepo struct{ db *gorm.DB }

func NewLogAccessStreamRepository(db *gorm.DB) LogAccessStreamRepository {
	return &logAccessStreamRepo{db: db}
}

func (r *logAccessStreamRepo) Create(ctx context.Context, stream *models.LogAccessStream) error {
	if err := r.db.WithContext(ctx).Create(stream).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *logAccessStreamRepo) FindByStreamKey(ctx context.Context, streamKey string) (*models.LogAccessStream, error) {
	var stream models.LogAccessStream
	if err := r.db.WithContext(ctx).Where("stream_key = ? AND expires_at > ?", streamKey, time.Now()).First(&stream).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &stream, nil
}

func (r *logAccessStreamRepo) DeleteByID(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Delete(&models.LogAccessStream{}, "id = ?", id)
	if err := result.Error; err != nil {
		return err
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *logAccessStreamRepo) CleanupExpired(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).Delete(&models.LogAccessStream{}, "expires_at <= ?", time.Now())
	if err := result.Error; err != nil {
		return 0, err
	}
	return result.RowsAffected, nil
}

func (r *logAccessStreamRepo) CountByUserID(ctx context.Context, userID string) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&models.LogAccessStream{}).Where("user_id = ? AND expires_at > ?", userID, time.Now()).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}