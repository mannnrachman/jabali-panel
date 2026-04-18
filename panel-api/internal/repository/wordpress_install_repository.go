package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// WordPressInstallRepository defines data access for WordPress installations.
type WordPressInstallRepository interface {
	Create(ctx context.Context, install *models.WordPressInstall) error
	FindByID(ctx context.Context, id string) (*models.WordPressInstall, error)
	FindByIDAndUserID(ctx context.Context, id, userID string) (*models.WordPressInstall, error)
	FindByDomainID(ctx context.Context, domainID string) (*models.WordPressInstall, error)
	FindByDBID(ctx context.Context, dbID string) (*models.WordPressInstall, error)
	ListByUserID(ctx context.Context, userID string, opts ListOptions) ([]models.WordPressInstall, int64, error)
	List(ctx context.Context, opts ListOptions) ([]models.WordPressInstall, int64, error)
	UpdateStatus(ctx context.Context, id, status string, lastError *string, version *string) error
	Delete(ctx context.Context, id string) error
}

type wordpressInstallRepo struct{ db *gorm.DB }

func NewWordPressInstallRepository(db *gorm.DB) WordPressInstallRepository {
	return &wordpressInstallRepo{db: db}
}

func (r *wordpressInstallRepo) Create(ctx context.Context, install *models.WordPressInstall) error {
	if err := r.db.WithContext(ctx).Create(install).Error; err != nil {
		return err
	}
	return nil
}

func (r *wordpressInstallRepo) FindByID(ctx context.Context, id string) (*models.WordPressInstall, error) {
	var install models.WordPressInstall
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&install).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &install, nil
}

func (r *wordpressInstallRepo) FindByIDAndUserID(ctx context.Context, id, userID string) (*models.WordPressInstall, error) {
	var install models.WordPressInstall
	if err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&install).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &install, nil
}

func (r *wordpressInstallRepo) FindByDomainID(ctx context.Context, domainID string) (*models.WordPressInstall, error) {
	var install models.WordPressInstall
	if err := r.db.WithContext(ctx).Where("domain_id = ?", domainID).First(&install).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &install, nil
}

func (r *wordpressInstallRepo) FindByDBID(ctx context.Context, dbID string) (*models.WordPressInstall, error) {
	var install models.WordPressInstall
	if err := r.db.WithContext(ctx).Where("db_id = ?", dbID).First(&install).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &install, nil
}

var wordpressInstallListCols = ListCols{
	Search:      []string{"admin_email"},
	Sort:        []string{"admin_email", "status", "created_at"},
	DefaultSort: "created_at",
}

func (r *wordpressInstallRepo) ListByUserID(ctx context.Context, userID string, opts ListOptions) ([]models.WordPressInstall, int64, error) {
	var (
		installs []models.WordPressInstall
		total    int64
	)
	base := r.db.WithContext(ctx).Model(&models.WordPressInstall{}).Where("user_id = ?", userID)

	countQ := applyListOptions(base.Session(&gorm.Session{}), ListOptions{Search: opts.Search}, wordpressInstallListCols)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if opts.Sort == "" && opts.Order == "" {
		opts.Order = "desc"
	}
	q := applyListOptions(base.Session(&gorm.Session{}), opts, wordpressInstallListCols)
	if err := q.Find(&installs).Error; err != nil {
		return nil, 0, err
	}
	return installs, total, nil
}

func (r *wordpressInstallRepo) List(ctx context.Context, opts ListOptions) ([]models.WordPressInstall, int64, error) {
	var (
		installs []models.WordPressInstall
		total    int64
	)
	base := r.db.WithContext(ctx).Model(&models.WordPressInstall{})

	countQ := applyListOptions(base.Session(&gorm.Session{}), ListOptions{Search: opts.Search}, wordpressInstallListCols)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if opts.Sort == "" && opts.Order == "" {
		opts.Order = "desc"
	}
	q := applyListOptions(base.Session(&gorm.Session{}), opts, wordpressInstallListCols)
	if err := q.Find(&installs).Error; err != nil {
		return nil, 0, err
	}
	return installs, total, nil
}

func (r *wordpressInstallRepo) UpdateStatus(ctx context.Context, id, status string, lastError *string, version *string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if lastError != nil {
		updates["last_error"] = *lastError
	}
	if version != nil {
		updates["version"] = *version
	}
	if err := r.db.WithContext(ctx).Model(&models.WordPressInstall{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return err
	}
	return nil
}

func (r *wordpressInstallRepo) Delete(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Delete(&models.WordPressInstall{}, "id = ?", id)
	if err := result.Error; err != nil {
		return err
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
