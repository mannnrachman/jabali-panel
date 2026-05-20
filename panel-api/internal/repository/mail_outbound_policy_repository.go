package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// MailOutboundPolicyRepository CRUD for the mail_outbound_policy
// table (M47 Wave 3). The reconciler reads + updates stalwart_id;
// admin REST reads + writes scope/max_per_*.
type MailOutboundPolicyRepository interface {
	Create(ctx context.Context, p *models.MailOutboundPolicy) error
	Update(ctx context.Context, p *models.MailOutboundPolicy) error
	FindByID(ctx context.Context, id string) (*models.MailOutboundPolicy, error)
	FindByScope(ctx context.Context, scope string, scopeRef *string) (*models.MailOutboundPolicy, error)
	List(ctx context.Context) ([]models.MailOutboundPolicy, error)
	Delete(ctx context.Context, id string) error
	// UpdateApplyState stamps the post-reconcile state. stalwartID is
	// the upstream-assigned id (or unchanged on update). lastError nil
	// = success.
	UpdateApplyState(ctx context.Context, id, stalwartID string, lastError *string) error
	// UpdateApplyStateDaily stamps the SECOND (daily) Stalwart id +
	// last_applied_at. last_error is shared with the hourly path.
	UpdateApplyStateDaily(ctx context.Context, id, stalwartIDDaily string, lastError *string) error
}

type mailOutboundPolicyRepo struct{ db *gorm.DB }

func NewMailOutboundPolicyRepository(db *gorm.DB) MailOutboundPolicyRepository {
	return &mailOutboundPolicyRepo{db: db}
}

func (r *mailOutboundPolicyRepo) Create(ctx context.Context, p *models.MailOutboundPolicy) error {
	if p.ID == "" {
		p.ID = ids.NewULID()
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if err := r.db.WithContext(ctx).Create(p).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *mailOutboundPolicyRepo) Update(ctx context.Context, p *models.MailOutboundPolicy) error {
	p.UpdatedAt = time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&models.MailOutboundPolicy{}).
		Where("id = ?", p.ID).
		Updates(map[string]any{
			"max_per_hour": p.MaxPerHour,
			"max_per_day":  p.MaxPerDay,
			"enabled":      p.Enabled,
			"updated_at":   p.UpdatedAt,
		})
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *mailOutboundPolicyRepo) FindByID(ctx context.Context, id string) (*models.MailOutboundPolicy, error) {
	var row models.MailOutboundPolicy
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, translate(err)
	}
	return &row, nil
}

func (r *mailOutboundPolicyRepo) FindByScope(ctx context.Context, scope string, scopeRef *string) (*models.MailOutboundPolicy, error) {
	var row models.MailOutboundPolicy
	q := r.db.WithContext(ctx).Where("scope = ?", scope)
	if scopeRef == nil {
		q = q.Where("scope_ref IS NULL")
	} else {
		q = q.Where("scope_ref = ?", *scopeRef)
	}
	err := q.First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, translate(err)
	}
	return &row, nil
}

func (r *mailOutboundPolicyRepo) List(ctx context.Context) ([]models.MailOutboundPolicy, error) {
	var rows []models.MailOutboundPolicy
	if err := r.db.WithContext(ctx).
		Order("scope, scope_ref").
		Find(&rows).Error; err != nil {
		return nil, translate(err)
	}
	return rows, nil
}

func (r *mailOutboundPolicyRepo) Delete(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Delete(&models.MailOutboundPolicy{}, "id = ?", id)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *mailOutboundPolicyRepo) UpdateApplyState(ctx context.Context, id, stalwartID string, lastError *string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&models.MailOutboundPolicy{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"stalwart_id":     stalwartID,
			"last_applied_at": now,
			"last_error":      lastError,
		})
	if res.Error != nil {
		return translate(res.Error)
	}
	return nil
}

func (r *mailOutboundPolicyRepo) UpdateApplyStateDaily(ctx context.Context, id, stalwartIDDaily string, lastError *string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&models.MailOutboundPolicy{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"stalwart_id_daily": stalwartIDDaily,
			"last_applied_at":   now,
			"last_error":        lastError,
		})
	if res.Error != nil {
		return translate(res.Error)
	}
	return nil
}
