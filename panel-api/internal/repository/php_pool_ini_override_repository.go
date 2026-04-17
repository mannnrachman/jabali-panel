package repository

import (
	"context"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// PHPPoolIniOverrideRepository defines data access for PHP pool ini overrides.
type PHPPoolIniOverrideRepository interface {
	Create(ctx context.Context, o *models.PHPPoolIniOverride) error
	FindByID(ctx context.Context, id string) (*models.PHPPoolIniOverride, error)
	ListByPool(ctx context.Context, poolID string) ([]models.PHPPoolIniOverride, error)
	Update(ctx context.Context, o *models.PHPPoolIniOverride) error
	Delete(ctx context.Context, id string) error
}

type phpPoolIniOverrideRepo struct{ db *gorm.DB }

func NewPHPPoolIniOverrideRepository(db *gorm.DB) PHPPoolIniOverrideRepository {
	return &phpPoolIniOverrideRepo{db: db}
}

func (r *phpPoolIniOverrideRepo) Create(ctx context.Context, o *models.PHPPoolIniOverride) error {
	return r.db.WithContext(ctx).Create(o).Error
}

func (r *phpPoolIniOverrideRepo) FindByID(ctx context.Context, id string) (*models.PHPPoolIniOverride, error) {
	var override models.PHPPoolIniOverride
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&override).Error; err != nil {
		return nil, err
	}
	return &override, nil
}

func (r *phpPoolIniOverrideRepo) ListByPool(ctx context.Context, poolID string) ([]models.PHPPoolIniOverride, error) {
	var overrides []models.PHPPoolIniOverride
	if err := r.db.WithContext(ctx).
		Where("pool_id = ?", poolID).
		Order("created_at ASC").
		Find(&overrides).Error; err != nil {
		return nil, err
	}
	return overrides, nil
}

func (r *phpPoolIniOverrideRepo) Update(ctx context.Context, o *models.PHPPoolIniOverride) error {
	return r.db.WithContext(ctx).Save(o).Error
}

func (r *phpPoolIniOverrideRepo) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&models.PHPPoolIniOverride{}).Error
}
