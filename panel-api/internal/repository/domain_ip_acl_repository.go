package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// DomainIPACLRepository persists per-domain IP allow/deny rules (M36).
// Reconciler reads ListByDomain to render the per-domain nginx snippet
// every time it converges a domain.
type DomainIPACLRepository interface {
	Create(ctx context.Context, row *models.DomainIPACL) error
	FindByID(ctx context.Context, id string) (*models.DomainIPACL, error)
	ListByDomain(ctx context.Context, domainID string) ([]models.DomainIPACL, error)
	Delete(ctx context.Context, id string) error
}

type domainIPACLRepo struct{ db *gorm.DB }

func NewDomainIPACLRepository(db *gorm.DB) DomainIPACLRepository {
	return &domainIPACLRepo{db: db}
}

func (r *domainIPACLRepo) Create(ctx context.Context, row *models.DomainIPACL) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(row).Error
}

func (r *domainIPACLRepo) FindByID(ctx context.Context, id string) (*models.DomainIPACL, error) {
	var row models.DomainIPACL
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *domainIPACLRepo) ListByDomain(ctx context.Context, domainID string) ([]models.DomainIPACL, error) {
	var rows []models.DomainIPACL
	err := r.db.WithContext(ctx).
		Where("domain_id = ?", domainID).
		Order("priority ASC, created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *domainIPACLRepo) Delete(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Delete(&models.DomainIPACL{}, "id = ?", id)
	if err := res.Error; err != nil {
		return err
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
