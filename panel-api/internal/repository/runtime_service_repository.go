package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// RuntimeServiceRepository defines data access for non-PHP runtime
// services (Node.js, Python, Go, Docker). Follows the same interface
// conventions as PHPPoolRepository: CRUD + status setter + finder
// variants.
type RuntimeServiceRepository interface {
	Create(ctx context.Context, s *models.RuntimeService) error
	FindByID(ctx context.Context, id string) (*models.RuntimeService, error)
	FindByDomainID(ctx context.Context, domainID string) (*models.RuntimeService, error)
	FindByUserID(ctx context.Context, userID string) ([]models.RuntimeService, error)
	ListAll(ctx context.Context, opts ListOptions) ([]models.RuntimeService, int64, error)
	ListByStatus(ctx context.Context, status string) ([]models.RuntimeService, error)
	Update(ctx context.Context, s *models.RuntimeService) error
	Delete(ctx context.Context, id string) error
	SetStatus(ctx context.Context, id, status string, lastErr *string) error
	// IsPortInUse reports whether any runtime service is already
	// listening on the given port. Used by the port allocator to
	// avoid collisions.
	IsPortInUse(ctx context.Context, port uint32) (bool, error)
}

type runtimeServiceRepo struct{ db *gorm.DB }

// NewRuntimeServiceRepository returns a GORM-backed implementation.
func NewRuntimeServiceRepository(db *gorm.DB) RuntimeServiceRepository {
	return &runtimeServiceRepo{db: db}
}

func (r *runtimeServiceRepo) Create(ctx context.Context, s *models.RuntimeService) error {
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *runtimeServiceRepo) FindByID(ctx context.Context, id string) (*models.RuntimeService, error) {
	var svc models.RuntimeService
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&svc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &svc, nil
}

func (r *runtimeServiceRepo) FindByDomainID(ctx context.Context, domainID string) (*models.RuntimeService, error) {
	var svc models.RuntimeService
	if err := r.db.WithContext(ctx).Where("domain_id = ?", domainID).First(&svc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &svc, nil
}

func (r *runtimeServiceRepo) FindByUserID(ctx context.Context, userID string) ([]models.RuntimeService, error) {
	var svcs []models.RuntimeService
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&svcs).Error; err != nil {
		return nil, err
	}
	return svcs, nil
}

func (r *runtimeServiceRepo) ListAll(ctx context.Context, opts ListOptions) ([]models.RuntimeService, int64, error) {
	var svcs []models.RuntimeService
	var total int64

	q := r.db.WithContext(ctx)

	if err := q.Model(&models.RuntimeService{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}

	q = q.Order("created_at DESC")

	if err := q.Find(&svcs).Error; err != nil {
		return nil, 0, err
	}
	return svcs, total, nil
}

func (r *runtimeServiceRepo) ListByStatus(ctx context.Context, status string) ([]models.RuntimeService, error) {
	var svcs []models.RuntimeService
	if err := r.db.WithContext(ctx).Where("status = ?", status).Find(&svcs).Error; err != nil {
		return nil, err
	}
	return svcs, nil
}

func (r *runtimeServiceRepo) Update(ctx context.Context, s *models.RuntimeService) error {
	return r.db.WithContext(ctx).Save(s).Error
}

func (r *runtimeServiceRepo) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&models.RuntimeService{}).Error
}

func (r *runtimeServiceRepo) SetStatus(ctx context.Context, id, status string, lastErr *string) error {
	return r.db.WithContext(ctx).Model(&models.RuntimeService{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     status,
			"last_error": lastErr,
		}).Error
}

func (r *runtimeServiceRepo) IsPortInUse(ctx context.Context, port uint32) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&models.RuntimeService{}).
		Where("listen_port = ?", port).
		Count(&count).Error
	return count > 0, err
}
