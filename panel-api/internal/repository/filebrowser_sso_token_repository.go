package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// FileBrowserSSOTokenRepository defines data access for filebrowser SSO tokens.
type FileBrowserSSOTokenRepository interface {
	// Create inserts a new SSO token into the database.
	Create(ctx context.Context, t *models.FileBrowserSSOToken) error

	// FindByHash retrieves an unexpired token by its hash.
	// Returns ErrNotFound if no row matches or the row is expired/already-used.
	FindByHash(ctx context.Context, tokenHash string) (*models.FileBrowserSSOToken, error)

	// MarkUsed sets used_at to NOW() for the token with the given ID.
	MarkUsed(ctx context.Context, tokenID string) error

	// DeleteExpired deletes all tokens where expires_at <= now and returns
	// the count of deleted rows.
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

type fileBrowserSSOTokenRepo struct{ db *gorm.DB }

// NewFileBrowserSSOTokenRepository creates a new instance of the filebrowser SSO token repository.
func NewFileBrowserSSOTokenRepository(db *gorm.DB) FileBrowserSSOTokenRepository {
	return &fileBrowserSSOTokenRepo{db: db}
}

// Create inserts a new SSO token.
func (r *fileBrowserSSOTokenRepo) Create(ctx context.Context, t *models.FileBrowserSSOToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// FindByHash retrieves a token by its hash if it exists and is not expired/used.
func (r *fileBrowserSSOTokenRepo) FindByHash(ctx context.Context, tokenHash string) (*models.FileBrowserSSOToken, error) {
	var token models.FileBrowserSSOToken
	now := time.Now()

	err := r.db.WithContext(ctx).
		Where("token_hash = ? AND expires_at > ? AND used_at IS NULL", tokenHash, now).
		First(&token).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return &token, nil
}

// MarkUsed sets used_at to NOW() for the token with the given ID.
func (r *fileBrowserSSOTokenRepo) MarkUsed(ctx context.Context, tokenID string) error {
	return r.db.WithContext(ctx).
		Model(&models.FileBrowserSSOToken{}).
		Where("id = ?", tokenID).
		Update("used_at", time.Now()).
		Error
}

// DeleteExpired deletes all expired tokens and returns the count deleted.
func (r *fileBrowserSSOTokenRepo) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	result := r.db.WithContext(ctx).
		Where("expires_at <= ?", before).
		Delete(&models.FileBrowserSSOToken{})
	return result.RowsAffected, result.Error
}
