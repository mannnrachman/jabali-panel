package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// MailboxSSOTokenRepository is the data-access layer for webmail-SSO
// tokens (M6 Step 8 Phase B). Shape mirrors PhpMyAdminSSOTokenRepository
// deliberately — same invariants (single-use, hashed, short-lived) so
// the attacker model is identical and the code review burden is low.
type MailboxSSOTokenRepository interface {
	Create(ctx context.Context, t *models.MailboxSSOToken) error
	// ConsumeByHash atomically finds the unexpired token with the given
	// SHA-256 hash, deletes it, and returns the snapshot. Returns
	// ErrNotFound when the token is unknown, already consumed, or
	// expired (caller treats all three the same to deny oracles).
	ConsumeByHash(ctx context.Context, tokenHash string) (*models.MailboxSSOToken, error)
	// PurgeExpired is the nightly sweep — matches the existing phpMyAdmin
	// SSO tokens sweep driven by the reconciler prune ticker.
	PurgeExpired(ctx context.Context) (int64, error)
}

type mailboxSSOTokenRepo struct{ db *gorm.DB }

func NewMailboxSSOTokenRepository(db *gorm.DB) MailboxSSOTokenRepository {
	return &mailboxSSOTokenRepo{db: db}
}

func (r *mailboxSSOTokenRepo) Create(ctx context.Context, t *models.MailboxSSOToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *mailboxSSOTokenRepo) ConsumeByHash(ctx context.Context, tokenHash string) (*models.MailboxSSOToken, error) {
	var token models.MailboxSSOToken
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
		if err := tx.Delete(&token).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func (r *mailboxSSOTokenRepo) PurgeExpired(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).
		Where("expires_at <= ?", time.Now()).
		Delete(&models.MailboxSSOToken{})
	return result.RowsAffected, result.Error
}
