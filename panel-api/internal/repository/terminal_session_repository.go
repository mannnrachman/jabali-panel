package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// TerminalSessionRepository — one-shot root-terminal session tokens (M45).
type TerminalSessionRepository interface {
	Create(ctx context.Context, s *models.TerminalSession) error
	// ConsumeValid atomically finds an unexpired, unused token and stamps
	// UsedAt in the same UPDATE, so a token can be redeemed exactly once
	// even under concurrent WS upgrades. Returns the row as it was just
	// before consumption (for IP / user_id verification by the caller).
	ConsumeValid(ctx context.Context, token string) (*models.TerminalSession, error)
	MarkStarted(ctx context.Context, id, castPath string) error
	MarkEnded(ctx context.Context, id string) error
	CleanupExpired(ctx context.Context) (int64, error)
}

type terminalSessionRepo struct{ db *gorm.DB }

func NewTerminalSessionRepository(db *gorm.DB) TerminalSessionRepository {
	return &terminalSessionRepo{db: db}
}

func (r *terminalSessionRepo) Create(ctx context.Context, s *models.TerminalSession) error {
	if err := r.db.WithContext(ctx).Create(s).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *terminalSessionRepo) ConsumeValid(ctx context.Context, token string) (*models.TerminalSession, error) {
	now := time.Now()
	var s models.TerminalSession
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Single-use + unexpired gate enforced in the UPDATE's WHERE so
		// two concurrent upgrades can't both win.
		res := tx.Model(&models.TerminalSession{}).
			Where("token = ? AND used_at IS NULL AND expires_at > ?", token, now).
			Update("used_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound // missing, expired, or already consumed
		}
		return tx.Where("token = ?", token).First(&s).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

func (r *terminalSessionRepo) MarkStarted(ctx context.Context, id, castPath string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&models.TerminalSession{}).
		Where("id = ?", id).
		Updates(map[string]any{"started_at": now, "cast_path": castPath}).Error
}

func (r *terminalSessionRepo) MarkEnded(ctx context.Context, id string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&models.TerminalSession{}).
		Where("id = ?", id).
		Update("ended_at", now).Error
}

func (r *terminalSessionRepo) CleanupExpired(ctx context.Context) (int64, error) {
	// Drop only fully-finished or never-connected expired rows; keep
	// rows with a StartedAt but no EndedAt (a live or crashed session)
	// for forensic correlation with the .cast file.
	res := r.db.WithContext(ctx).
		Where("expires_at <= ? AND started_at IS NULL", time.Now()).
		Delete(&models.TerminalSession{})
	return res.RowsAffected, res.Error
}
