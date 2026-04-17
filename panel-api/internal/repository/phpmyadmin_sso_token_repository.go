package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// PhpMyAdminSSOTokenRepository defines data access for phpMyAdmin SSO tokens.
type PhpMyAdminSSOTokenRepository interface {
	// Create inserts a new SSO token into the database.
	Create(ctx context.Context, t *models.PhpMyAdminSSOToken) error

	// ConsumeByHash atomically selects the unexpired row matching tokenHash,
	// deletes it, and returns the snapshot. Returns ErrNotFound if no row
	// matches or the row is expired (already-consumed rows are indistinguishable
	// from unknown rows, which is intentional — prevents oracle attacks).
	ConsumeByHash(ctx context.Context, tokenHash string) (*models.PhpMyAdminSSOToken, error)

	// PurgeExpired deletes all tokens where expires_at <= now and returns
	// the count of deleted rows.
	PurgeExpired(ctx context.Context) (int64, error)
}

type phpMyAdminSSOTokenRepo struct{ db *gorm.DB }

// NewPhpMyAdminSSOTokenRepository creates a new instance of the SSO token repository.
func NewPhpMyAdminSSOTokenRepository(db *gorm.DB) PhpMyAdminSSOTokenRepository {
	return &phpMyAdminSSOTokenRepo{db: db}
}

// Create inserts a new SSO token.
func (r *phpMyAdminSSOTokenRepo) Create(ctx context.Context, t *models.PhpMyAdminSSOToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// ConsumeByHash atomically selects and deletes a token by its hash.
// The SELECT + DELETE run within a single transaction with FOR UPDATE locking
// to prevent race conditions (e.g., two concurrent consume calls on the same token).
func (r *phpMyAdminSSOTokenRepo) ConsumeByHash(ctx context.Context, tokenHash string) (*models.PhpMyAdminSSOToken, error) {
	var token models.PhpMyAdminSSOToken
	now := time.Now()

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// SELECT with FOR UPDATE lock to prevent concurrent consumes.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ? AND expires_at > ?", tokenHash, now).
			First(&token).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		// Delete the token within the same transaction while we hold the lock.
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

// PurgeExpired deletes all expired tokens and returns the count deleted.
func (r *phpMyAdminSSOTokenRepo) PurgeExpired(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).Where("expires_at <= ?", time.Now()).
		Delete(&models.PhpMyAdminSSOToken{})
	return result.RowsAffected, result.Error
}
