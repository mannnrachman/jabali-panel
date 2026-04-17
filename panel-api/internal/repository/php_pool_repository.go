package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// PHPPoolRepository defines data access for PHP-FPM pools.
type PHPPoolRepository interface {
	Create(ctx context.Context, p *models.PHPPool) error
	FindByID(ctx context.Context, id string) (*models.PHPPool, error)
	FindByUserID(ctx context.Context, userID string) (*models.PHPPool, error)
	ListAll(ctx context.Context, opts ListOptions) ([]models.PHPPool, int64, error)
	Update(ctx context.Context, p *models.PHPPool) error
	Delete(ctx context.Context, id string) error
	SetStatus(ctx context.Context, id, status string, lastErr *string) error
}

type phpPoolRepo struct{ db *gorm.DB }

func NewPHPPoolRepository(db *gorm.DB) PHPPoolRepository {
	return &phpPoolRepo{db: db}
}

func (r *phpPoolRepo) Create(ctx context.Context, p *models.PHPPool) error {
	return r.db.WithContext(ctx).Create(p).Error
}

func (r *phpPoolRepo) FindByID(ctx context.Context, id string) (*models.PHPPool, error) {
	var pool models.PHPPool
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&pool).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &pool, nil
}

func (r *phpPoolRepo) FindByUserID(ctx context.Context, userID string) (*models.PHPPool, error) {
	var pool models.PHPPool
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&pool).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &pool, nil
}

func (r *phpPoolRepo) ListAll(ctx context.Context, opts ListOptions) ([]models.PHPPool, int64, error) {
	var pools []models.PHPPool
	var total int64

	q := r.db.WithContext(ctx)

	// Count total rows
	if err := q.Model(&models.PHPPool{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Apply pagination
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}

	// Default sort by created_at DESC
	q = q.Order("created_at DESC")

	if err := q.Find(&pools).Error; err != nil {
		return nil, 0, err
	}

	return pools, total, nil
}

func (r *phpPoolRepo) Update(ctx context.Context, p *models.PHPPool) error {
	return r.db.WithContext(ctx).Save(p).Error
}

func (r *phpPoolRepo) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&models.PHPPool{}).Error
}

func (r *phpPoolRepo) SetStatus(ctx context.Context, id, status string, lastErr *string) error {
	return r.db.WithContext(ctx).Model(&models.PHPPool{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     status,
			"last_error": lastErr,
		}).Error
}
