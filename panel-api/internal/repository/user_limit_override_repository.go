package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// UserLimitOverrideRepository is the data layer for `user_limit_overrides`.
// The table is keyed by user_id (one override row per user, or zero);
// FindByUserID returning (nil, ErrNotFound) is the expected "no override"
// signal and is NOT an error condition for the resolver.
type UserLimitOverrideRepository interface {
	FindByUserID(ctx context.Context, userID string) (*models.UserLimitOverride, error)
	Upsert(ctx context.Context, o *models.UserLimitOverride) error
	Delete(ctx context.Context, userID string) error
	// ListAll returns every override row, used by the reconciler to build
	// the effective-limits table for a whole host in one DB pass.
	ListAll(ctx context.Context) ([]models.UserLimitOverride, error)
}

type userLimitOverrideRepo struct{ db *gorm.DB }

func NewUserLimitOverrideRepository(db *gorm.DB) UserLimitOverrideRepository {
	return &userLimitOverrideRepo{db: db}
}

func (r *userLimitOverrideRepo) FindByUserID(ctx context.Context, userID string) (*models.UserLimitOverride, error) {
	var o models.UserLimitOverride
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&o).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &o, nil
}

// Upsert uses INSERT ... ON DUPLICATE KEY UPDATE semantics — either the
// row doesn't exist and we insert it, or it does and we overwrite every
// override column. The updated_at column is bumped by MySQL's ON UPDATE.
func (r *userLimitOverrideRepo) Upsert(ctx context.Context, o *models.UserLimitOverride) error {
	// Save does an INSERT-or-UPDATE based on primary-key presence.
	// For a table keyed solely on user_id with NULL-able fields, this
	// is exactly what we want — GORM emits UPDATE ... SET ... for
	// every column including the NULLs, which is the semantic we need
	// (setting a field back to NULL = "inherit from package").
	if err := r.db.WithContext(ctx).Save(o).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *userLimitOverrideRepo) Delete(ctx context.Context, userID string) error {
	res := r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&models.UserLimitOverride{})
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *userLimitOverrideRepo) ListAll(ctx context.Context) ([]models.UserLimitOverride, error) {
	var out []models.UserLimitOverride
	if err := r.db.WithContext(ctx).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}
