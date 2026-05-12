package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// EmailFilterRepository persists per-mailbox inbox filter rules.
type EmailFilterRepository interface {
	FindByMailboxID(ctx context.Context, mailboxID string) ([]models.EmailFilter, error)
	Create(ctx context.Context, f *models.EmailFilter) error
	Delete(ctx context.Context, id string) error
}

type emailFilterRepo struct {
	db *gorm.DB
}

func NewEmailFilterRepository(db *gorm.DB) EmailFilterRepository {
	return &emailFilterRepo{db: db}
}

func (r *emailFilterRepo) FindByMailboxID(ctx context.Context, mailboxID string) ([]models.EmailFilter, error) {
	var rows []models.EmailFilter
	tx := r.db.WithContext(ctx).Where("mailbox_id = ?", mailboxID).Order("priority ASC, name ASC")
	if err := tx.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *emailFilterRepo) Create(ctx context.Context, f *models.EmailFilter) error {
	now := time.Now().UTC()
	f.CreatedAt = now
	f.UpdatedAt = now
	if err := r.db.WithContext(ctx).Create(f).Error; err != nil {
		// MySQL 1062 = duplicate uniq_mailbox_filter_name. Surface as
		// ErrConflict so callers can skip-on-rerun without surfacing a
		// 500.
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrConflict
		}
		return err
	}
	return nil
}

func (r *emailFilterRepo) Delete(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Delete(&models.EmailFilter{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
