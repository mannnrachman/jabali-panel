// Package repository — BackupDestinationRepository owns backup_destinations.
// M30.1 / ADR-0078.
package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

type BackupDestinationRepository interface {
	Create(ctx context.Context, d *models.BackupDestination) error
	Get(ctx context.Context, id string) (*models.BackupDestination, error)
	GetByName(ctx context.Context, name string) (*models.BackupDestination, error)
	List(ctx context.Context) ([]models.BackupDestination, error)
	ListEnabled(ctx context.Context) ([]models.BackupDestination, error)
	Update(ctx context.Context, d *models.BackupDestination) error
	Delete(ctx context.Context, id string) error
}

type backupDestinationRepo struct{ db *gorm.DB }

func NewBackupDestinationRepository(db *gorm.DB) BackupDestinationRepository {
	return &backupDestinationRepo{db: db}
}

func (r *backupDestinationRepo) Create(ctx context.Context, d *models.BackupDestination) error {
	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = now
	}
	if err := r.db.WithContext(ctx).Create(d).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *backupDestinationRepo) Get(ctx context.Context, id string) (*models.BackupDestination, error) {
	var out models.BackupDestination
	if err := r.db.WithContext(ctx).First(&out, "id = ?", id).Error; err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

func (r *backupDestinationRepo) GetByName(ctx context.Context, name string) (*models.BackupDestination, error) {
	var out models.BackupDestination
	if err := r.db.WithContext(ctx).First(&out, "name = ?", name).Error; err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

func (r *backupDestinationRepo) List(ctx context.Context) ([]models.BackupDestination, error) {
	var out []models.BackupDestination
	if err := r.db.WithContext(ctx).Order("name ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

func (r *backupDestinationRepo) ListEnabled(ctx context.Context) ([]models.BackupDestination, error) {
	var out []models.BackupDestination
	if err := r.db.WithContext(ctx).
		Where("enabled = ?", true).
		Order("name ASC").
		Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

func (r *backupDestinationRepo) Update(ctx context.Context, d *models.BackupDestination) error {
	d.UpdatedAt = time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&models.BackupDestination{}).
		Where("id = ?", d.ID).
		Updates(map[string]any{
			"name":            d.Name,
			"kind":            d.Kind,
			"url":             d.URL,
			"credentials_ref": d.CredentialsRef,
			"enabled":         d.Enabled,
			"updated_at":      d.UpdatedAt,
		})
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *backupDestinationRepo) Delete(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Delete(&models.BackupDestination{}, "id = ?", id)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
