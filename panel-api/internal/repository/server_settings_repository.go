package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// ServerSettingsRepository is the interface for accessing the single-row
// server_settings table. Implementations must enforce that only row id=1
// exists.
type ServerSettingsRepository interface {
	Get(ctx context.Context) (*models.ServerSettings, error)
	Upsert(ctx context.Context, s *models.ServerSettings) error
}

type serverSettingsRepo struct{ db *gorm.DB }

// NewServerSettingsRepository returns a ServerSettingsRepository backed by
// the given GORM handle.
func NewServerSettingsRepository(db *gorm.DB) ServerSettingsRepository {
	return &serverSettingsRepo{db: db}
}

func (r *serverSettingsRepo) Get(ctx context.Context) (*models.ServerSettings, error) {
	var s models.ServerSettings
	if err := r.db.WithContext(ctx).First(&s, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

// Upsert writes the row, creating it if missing. Forces ID=1.
// Uses explicit exists-check + Create/Updates instead of Save() because
// GORM's Save with the `uint8 primaryKey default:1` tag on MySQL 8 /
// MariaDB 10.11 generates SQL that lists the `id` column twice in the
// INSERT ... ON DUPLICATE KEY UPDATE clause, triggering error 1110
// ("Column 'id' specified twice").
func (r *serverSettingsRepo) Upsert(ctx context.Context, s *models.ServerSettings) error {
	s.ID = 1
	s.UpdatedAt = time.Now().UTC()

	var existing models.ServerSettings
	err := r.db.WithContext(ctx).First(&existing, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.WithContext(ctx).Create(s).Error
	}
	if err != nil {
		return err
	}
	// Select("*") forces all columns to be updated — including zero
	// values — so a caller that intentionally clears a field (e.g.
	// admin_email to "") gets the clear persisted. Omit("id") keeps
	// the primary key out of the SET clause.
	return r.db.WithContext(ctx).Model(&existing).Select("*").Omit("id").Updates(s).Error
}
