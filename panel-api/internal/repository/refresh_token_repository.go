package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// RefreshTokenRepository is the interface the auth service uses. A mock
// implementation of this interface is used in Phase 4 auth tests.
type RefreshTokenRepository interface {
	Create(ctx context.Context, t *models.RefreshToken) error
	FindByHash(ctx context.Context, tokenHash string) (*models.RefreshToken, error)
	Revoke(ctx context.Context, id string, at time.Time) error
	RevokeAllForUser(ctx context.Context, userID string, at time.Time) error

	// Rotate atomically revokes oldHash and inserts newToken. Implementations
	// MUST do this inside a SELECT ... FOR UPDATE transaction so concurrent
	// refreshes of the same token can't both succeed.
	Rotate(ctx context.Context, oldHash string, newToken *models.RefreshToken) error
}

type refreshTokenRepo struct{ db *gorm.DB }

// NewRefreshTokenRepository wraps *gorm.DB in a RefreshTokenRepository.
func NewRefreshTokenRepository(db *gorm.DB) RefreshTokenRepository {
	return &refreshTokenRepo{db: db}
}

func (r *refreshTokenRepo) Create(ctx context.Context, t *models.RefreshToken) error {
	if err := r.db.WithContext(ctx).Create(t).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *refreshTokenRepo) FindByHash(ctx context.Context, tokenHash string) (*models.RefreshToken, error) {
	var t models.RefreshToken
	err := r.db.WithContext(ctx).First(&t, "token_hash = ?", tokenHash).Error
	if err != nil {
		return nil, translate(err)
	}
	return &t, nil
}

func (r *refreshTokenRepo) Revoke(ctx context.Context, id string, at time.Time) error {
	res := r.db.WithContext(ctx).
		Model(&models.RefreshToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", at)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *refreshTokenRepo) RevokeAllForUser(ctx context.Context, userID string, at time.Time) error {
	return translate(r.db.WithContext(ctx).
		Model(&models.RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", at).Error)
}

func (r *refreshTokenRepo) Rotate(ctx context.Context, oldHash string, newToken *models.RefreshToken) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old models.RefreshToken
		// Lock the row so concurrent rotations serialise.
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ?", oldHash).
			First(&old).Error
		if err != nil {
			return translate(err)
		}
		if old.RevokedAt != nil {
			// Already rotated by a concurrent call — treat as not-found
			// so the handler rejects this refresh attempt.
			return ErrNotFound
		}

		now := time.Now().UTC()
		if err := tx.Model(&old).Update("revoked_at", now).Error; err != nil {
			return translate(err)
		}
		if err := tx.Create(newToken).Error; err != nil {
			return translate(err)
		}
		return nil
	})
}
