package repository

import (
	"context"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// YARACustomRuleRepository tracks admin-uploaded YARA rule files in
// /etc/jabali/yara/. Filesystem is the source of truth for content;
// this table records audit (who uploaded, sha256 at upload time) +
// the enabled flag mirrored to *.yar / *.yar.disabled rename.
type YARACustomRuleRepository interface {
	List(ctx context.Context) ([]models.YARACustomRule, error)
	Get(ctx context.Context, filename string) (*models.YARACustomRule, error)
	Upsert(ctx context.Context, r *models.YARACustomRule) error
	SetEnabled(ctx context.Context, filename string, enabled bool) error
	Delete(ctx context.Context, filename string) error
}

type yaraCustomRuleRepo struct{ db *gorm.DB }

// NewYARACustomRuleRepository returns a GORM-backed YARA rule repo.
func NewYARACustomRuleRepository(db *gorm.DB) YARACustomRuleRepository {
	return &yaraCustomRuleRepo{db: db}
}

func (r *yaraCustomRuleRepo) List(ctx context.Context) ([]models.YARACustomRule, error) {
	var out []models.YARACustomRule
	if err := r.db.WithContext(ctx).
		Order("filename ASC").
		Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

func (r *yaraCustomRuleRepo) Get(ctx context.Context, filename string) (*models.YARACustomRule, error) {
	var out models.YARACustomRule
	if err := r.db.WithContext(ctx).
		Where("filename = ?", filename).
		First(&out).Error; err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

// Upsert inserts a fresh row or updates sha256+enabled on an existing
// filename. Used by the upload handler — same filename re-upload is a
// content replacement, not a duplicate.
func (r *yaraCustomRuleRepo) Upsert(ctx context.Context, rule *models.YARACustomRule) error {
	res := r.db.WithContext(ctx).
		Where("filename = ?", rule.Filename).
		Assign(map[string]any{
			"enabled":     rule.Enabled,
			"uploaded_by": rule.UploadedBy,
			"sha256":      rule.SHA256,
		}).
		FirstOrCreate(rule)
	if res.Error != nil {
		return translate(res.Error)
	}
	return nil
}

func (r *yaraCustomRuleRepo) SetEnabled(ctx context.Context, filename string, enabled bool) error {
	res := r.db.WithContext(ctx).
		Model(&models.YARACustomRule{}).
		Where("filename = ?", filename).
		Update("enabled", enabled)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *yaraCustomRuleRepo) Delete(ctx context.Context, filename string) error {
	res := r.db.WithContext(ctx).
		Where("filename = ?", filename).
		Delete(&models.YARACustomRule{})
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
