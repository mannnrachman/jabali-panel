package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// PackageRepository defines data access for hosting packages.
type PackageRepository interface {
	Create(ctx context.Context, p *models.HostingPackage) error
	FindByID(ctx context.Context, id string) (*models.HostingPackage, error)
	FindByName(ctx context.Context, name string) (*models.HostingPackage, error)
	List(ctx context.Context, offset, limit int) ([]models.HostingPackage, int64, error)
	Update(ctx context.Context, p *models.HostingPackage) error
	Delete(ctx context.Context, id string) error
}

type packageRepo struct{ db *gorm.DB }

func NewPackageRepository(db *gorm.DB) PackageRepository {
	return &packageRepo{db: db}
}

func (r *packageRepo) Create(ctx context.Context, p *models.HostingPackage) error {
	if err := r.db.WithContext(ctx).Create(p).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *packageRepo) FindByID(ctx context.Context, id string) (*models.HostingPackage, error) {
	var p models.HostingPackage
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (r *packageRepo) FindByName(ctx context.Context, name string) (*models.HostingPackage, error) {
	var p models.HostingPackage
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (r *packageRepo) List(ctx context.Context, offset, limit int) ([]models.HostingPackage, int64, error) {
	var (
		pkgs  []models.HostingPackage
		total int64
	)
	base := r.db.WithContext(ctx).Model(&models.HostingPackage{})
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := base.Order("name ASC").Offset(offset).Limit(limit).Find(&pkgs).Error; err != nil {
		return nil, 0, err
	}
	return pkgs, total, nil
}

func (r *packageRepo) Update(ctx context.Context, p *models.HostingPackage) error {
	if err := r.db.WithContext(ctx).Model(p).Where("id = ?", p.ID).Select(
		"name", "disk_quota_mb", "bandwidth_quota_mb", "max_domains",
		"max_email_accounts", "max_databases", "max_ftp_accounts",
		"ssh_enabled", "cgi_enabled", "updated_at",
	).Updates(p).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *packageRepo) Delete(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Delete(&models.HostingPackage{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
