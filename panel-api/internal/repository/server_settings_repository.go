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
func (r *serverSettingsRepo) Upsert(ctx context.Context, s *models.ServerSettings) error {
	s.ID = 1
	s.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Save(s).Error
}
