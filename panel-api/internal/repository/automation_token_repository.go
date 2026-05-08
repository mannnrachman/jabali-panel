package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// AutomationTokenRepository persists M44 HMAC-signed automation
// tokens. List endpoints surface non-secret fields only; the verify
// middleware uses FindByID + the row's secret_enc to recompute
// signatures.
type AutomationTokenRepository interface {
	Create(ctx context.Context, t *models.AutomationToken) error
	List(ctx context.Context) ([]models.AutomationToken, error)
	FindByID(ctx context.Context, id string) (*models.AutomationToken, error)
	Revoke(ctx context.Context, id string) error
	BumpLastUsed(ctx context.Context, id, ip string) error
}

type automationTokenRepo struct{ db *gorm.DB }

func NewAutomationTokenRepository(db *gorm.DB) AutomationTokenRepository {
	return &automationTokenRepo{db: db}
}

func (r *automationTokenRepo) Create(ctx context.Context, t *models.AutomationToken) error {
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *automationTokenRepo) List(ctx context.Context) ([]models.AutomationToken, error) {
	var rows []models.AutomationToken
	if err := r.db.WithContext(ctx).
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *automationTokenRepo) FindByID(ctx context.Context, id string) (*models.AutomationToken, error) {
	var t models.AutomationToken
	err := r.db.WithContext(ctx).First(&t, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *automationTokenRepo) Revoke(ctx context.Context, id string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).
		Model(&models.AutomationToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", now).Error
}

// BumpLastUsed updates last_used_at + last_used_ip best-effort. Errors
// are ignored at the call site (the middleware fires this in a
// goroutine and never blocks the verified request on its result).
func (r *automationTokenRepo) BumpLastUsed(ctx context.Context, id, ip string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).
		Model(&models.AutomationToken{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"last_used_at": now,
			"last_used_ip": ip,
		}).Error
}
