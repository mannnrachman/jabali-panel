package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// TOTPBackupCodeRepository covers the totp_backup_codes table.
type TOTPBackupCodeRepository interface {
	// CreateBatch inserts all codes for a user atomically. Use when
	// re-generating — caller is expected to DeleteAllByUserID first.
	CreateBatch(ctx context.Context, codes []models.TOTPBackupCode) error

	// ListUnusedByUserID returns every not-yet-redeemed code for a user.
	// The challenge handler iterates these, bcrypt-compares each, and
	// calls MarkUsed on the first match.
	ListUnusedByUserID(ctx context.Context, userID string) ([]models.TOTPBackupCode, error)

	// MarkUsed flips used_at to now on a single code. Idempotent: already-used
	// codes are a no-op (the handler treats that as an invalid code).
	MarkUsed(ctx context.Context, id string, now time.Time) error

	// DeleteAllByUserID removes every code for a user. Called on 2FA disable
	// and before a fresh regen-batch insert.
	DeleteAllByUserID(ctx context.Context, userID string) error
}

type totpBackupCodeRepo struct{ db *gorm.DB }

func NewTOTPBackupCodeRepository(db *gorm.DB) TOTPBackupCodeRepository {
	return &totpBackupCodeRepo{db: db}
}

func (r *totpBackupCodeRepo) CreateBatch(ctx context.Context, codes []models.TOTPBackupCode) error {
	if len(codes) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&codes).Error
}

func (r *totpBackupCodeRepo) ListUnusedByUserID(ctx context.Context, userID string) ([]models.TOTPBackupCode, error) {
	var out []models.TOTPBackupCode
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND used_at IS NULL", userID).
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *totpBackupCodeRepo) MarkUsed(ctx context.Context, id string, now time.Time) error {
	res := r.db.WithContext(ctx).
		Model(&models.TOTPBackupCode{}).
		Where("id = ? AND used_at IS NULL", id).
		Update("used_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("backup code not found or already used")
	}
	return nil
}

func (r *totpBackupCodeRepo) DeleteAllByUserID(ctx context.Context, userID string) error {
	return r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&models.TOTPBackupCode{}).Error
}
