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
	List(ctx context.Context, opts ListOptions) ([]models.HostingPackage, int64, error)
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

// packageListCols — packages only have a meaningful name field for
// search. Sort allows name/created_at. Historical default was name ASC.
var packageListCols = ListCols{
	Search:      []string{"name"},
	Sort:        []string{"name", "created_at"},
	DefaultSort: "name",
}

func (r *packageRepo) List(ctx context.Context, opts ListOptions) ([]models.HostingPackage, int64, error) {
	var (
		pkgs  []models.HostingPackage
		total int64
	)
	base := r.db.WithContext(ctx).Model(&models.HostingPackage{})

	countQ := applyListOptions(base.Session(&gorm.Session{}), ListOptions{Search: opts.Search}, packageListCols)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// When caller didn't ask for a sort direction, preserve the historical
	// alphabetical-ASC default rather than applying the generic DESC.
	if opts.Sort == "" && opts.Order == "" {
		opts.Order = "asc"
	}
	q := applyListOptions(base.Session(&gorm.Session{}), opts, packageListCols)
	if err := q.Find(&pkgs).Error; err != nil {
		return nil, 0, err
	}
	return pkgs, total, nil
}

func (r *packageRepo) Update(ctx context.Context, p *models.HostingPackage) error {
	if err := r.db.WithContext(ctx).Model(p).Where("id = ?", p.ID).Select(
		"name", "disk_quota_mb", "bandwidth_quota_mb", "max_domains",
		"max_email_accounts", "max_databases", "max_ftp_accounts",
		"ssh_enabled", "cgi_enabled", "nspawn_image_version", "updated_at",
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
