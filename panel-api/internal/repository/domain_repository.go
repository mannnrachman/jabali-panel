package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// DomainRepository defines data access for hosted domains.
type DomainRepository interface {
	Create(ctx context.Context, d *models.Domain) error
	FindByID(ctx context.Context, id string) (*models.Domain, error)
	FindByName(ctx context.Context, name string) (*models.Domain, error)
	List(ctx context.Context, opts ListOptions) ([]models.Domain, int64, error)
	ListByUserID(ctx context.Context, userID string, opts ListOptions) ([]models.Domain, int64, error)
	Update(ctx context.Context, d *models.Domain) error
	Delete(ctx context.Context, id string) error
	CountByUserID(ctx context.Context, userID string) (int64, error)
}

type domainRepo struct{ db *gorm.DB }

func NewDomainRepository(db *gorm.DB) DomainRepository {
	return &domainRepo{db: db}
}

func (r *domainRepo) Create(ctx context.Context, d *models.Domain) error {
	if err := r.db.WithContext(ctx).Create(d).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *domainRepo) FindByID(ctx context.Context, id string) (*models.Domain, error) {
	var d models.Domain
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&d).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

func (r *domainRepo) FindByName(ctx context.Context, name string) (*models.Domain, error) {
	var d models.Domain
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&d).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

// domainListCols — only the domain name is free-text searchable.
// user_id is deliberately absent from Search because it's an opaque ULID
// and admins have a dedicated "this user's domains" path via ListByUserID.
var domainListCols = ListCols{
	Search:      []string{"name"},
	Sort:        []string{"name", "created_at"},
	DefaultSort: "name",
}

func (r *domainRepo) List(ctx context.Context, opts ListOptions) ([]models.Domain, int64, error) {
	var (
		domains []models.Domain
		total   int64
	)
	base := r.db.WithContext(ctx).Model(&models.Domain{})

	countQ := applyListOptions(base.Session(&gorm.Session{}), ListOptions{Search: opts.Search}, domainListCols)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if opts.Sort == "" && opts.Order == "" {
		opts.Order = "asc"
	}
	q := applyListOptions(base.Session(&gorm.Session{}), opts, domainListCols)
	if err := q.Find(&domains).Error; err != nil {
		return nil, 0, err
	}
	return domains, total, nil
}

func (r *domainRepo) ListByUserID(ctx context.Context, userID string, opts ListOptions) ([]models.Domain, int64, error) {
	var (
		domains []models.Domain
		total   int64
	)
	base := r.db.WithContext(ctx).Model(&models.Domain{}).Where("user_id = ?", userID)

	countQ := applyListOptions(base.Session(&gorm.Session{}), ListOptions{Search: opts.Search}, domainListCols)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if opts.Sort == "" && opts.Order == "" {
		opts.Order = "asc"
	}
	q := applyListOptions(base.Session(&gorm.Session{}), opts, domainListCols)
	if err := q.Find(&domains).Error; err != nil {
		return nil, 0, err
	}
	return domains, total, nil
}

func (r *domainRepo) Update(ctx context.Context, d *models.Domain) error {
	if err := r.db.WithContext(ctx).Model(d).Where("id = ?", d.ID).Select(
		"name", "doc_root", "is_enabled", "nginx_custom_directives", "updated_at",
	).Updates(d).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *domainRepo) Delete(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Delete(&models.Domain{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *domainRepo) CountByUserID(ctx context.Context, userID string) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&models.Domain{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
