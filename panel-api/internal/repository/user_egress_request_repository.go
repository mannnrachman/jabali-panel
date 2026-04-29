package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// UserEgressRequestRepository owns user_egress_requests — the queue of
// user-submitted destinations awaiting admin review. Approval flips
// status to 'approved' and the calling handler is responsible for
// folding the destination into user_egress_policies.allowed_extra.
type UserEgressRequestRepository interface {
	Create(ctx context.Context, r *models.UserEgressRequest) error
	Get(ctx context.Context, id string) (*models.UserEgressRequest, error)
	ListPending(ctx context.Context) ([]models.UserEgressRequest, error)
	ListByUser(ctx context.Context, userID string) ([]models.UserEgressRequest, error)
	Decide(ctx context.Context, id string, status string, reviewedBy string) error
	CancelPending(ctx context.Context, id, userID string) error
}

type userEgressRequestRepo struct{ db *gorm.DB }

// NewUserEgressRequestRepository returns a GORM-backed repo.
func NewUserEgressRequestRepository(db *gorm.DB) UserEgressRequestRepository {
	return &userEgressRequestRepo{db: db}
}

// Create writes a brand-new pending request. ID + CreatedAt are caller-
// supplied (or zero-value, in which case GORM defaults take over).
func (r *userEgressRequestRepo) Create(ctx context.Context, req *models.UserEgressRequest) error {
	if req.Status == "" {
		req.Status = models.UserEgressRequestStatusPending
	}
	if req.Protocol == "" {
		req.Protocol = models.UserEgressProtocolTCP
	}
	if err := r.db.WithContext(ctx).Create(req).Error; err != nil {
		return translate(err)
	}
	return nil
}

// Get returns one request by ID.
func (r *userEgressRequestRepo) Get(ctx context.Context, id string) (*models.UserEgressRequest, error) {
	var row models.UserEgressRequest
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, translate(err)
	}
	return &row, nil
}

// ListPending returns every pending request ordered oldest-first so the
// admin queue UI shows the back-of-line entries first.
func (r *userEgressRequestRepo) ListPending(ctx context.Context) ([]models.UserEgressRequest, error) {
	var rows []models.UserEgressRequest
	err := r.db.WithContext(ctx).
		Where("status = ?", models.UserEgressRequestStatusPending).
		Order("created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, translate(err)
	}
	return rows, nil
}

// ListByUser returns every request belonging to one user, any status,
// most recent first. Powers /me/egress/requests.
func (r *userEgressRequestRepo) ListByUser(ctx context.Context, userID string) ([]models.UserEgressRequest, error) {
	var rows []models.UserEgressRequest
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, translate(err)
	}
	return rows, nil
}

// Decide flips a pending request to approved or denied. No-op if the
// row is already decided (idempotent) — RowsAffected reflects which.
func (r *userEgressRequestRepo) Decide(ctx context.Context, id string, status string, reviewedBy string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&models.UserEgressRequest{}).
		Where("id = ? AND status = ?", id, models.UserEgressRequestStatusPending).
		Updates(map[string]any{
			"status":      status,
			"reviewed_by": reviewedBy,
			"decided_at":  now,
		})
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// CancelPending hard-deletes a pending request belonging to userID.
// Returns ErrNotFound if no pending row matches (so the user cannot
// cancel another user's row, nor a row already approved/denied).
func (r *userEgressRequestRepo) CancelPending(ctx context.Context, id, userID string) error {
	res := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ? AND status = ?",
			id, userID, models.UserEgressRequestStatusPending).
		Delete(&models.UserEgressRequest{})
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
