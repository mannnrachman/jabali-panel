package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// AdminerSSOTokenRepository is the data-access surface for Adminer
// SSO tokens. Mirror of PhpMyAdminSSOTokenRepository (mig 000027) —
// keeping it a separate type lets the validate handler resolve a
// model with an `engine` discriminator without touching the
// pre-existing PMA flow.
type AdminerSSOTokenRepository interface {
	Create(ctx context.Context, t *models.AdminerSSOToken) error
	ConsumeByHash(ctx context.Context, tokenHash string) (*models.AdminerSSOToken, error)
	PurgeExpired(ctx context.Context) (int64, error)
}

type adminerSSOTokenRepo struct{ db *gorm.DB }

func NewAdminerSSOTokenRepository(db *gorm.DB) AdminerSSOTokenRepository {
	return &adminerSSOTokenRepo{db: db}
}

func (r *adminerSSOTokenRepo) Create(ctx context.Context, t *models.AdminerSSOToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// ConsumeByHash atomically selects + deletes the token matching the
// hash inside a FOR UPDATE transaction. Returns ErrNotFound when the
// row is missing OR expired — the validate path treats either as a
// hard reject without leaking the distinction.
func (r *adminerSSOTokenRepo) ConsumeByHash(ctx context.Context, tokenHash string) (*models.AdminerSSOToken, error) {
	var token models.AdminerSSOToken
	now := time.Now()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ? AND expires_at > ?", tokenHash, now).
			First(&token).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		return tx.Delete(&token).Error
	})
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func (r *adminerSSOTokenRepo) PurgeExpired(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).Where("expires_at <= ?", time.Now()).
		Delete(&models.AdminerSSOToken{})
	return result.RowsAffected, result.Error
}
