package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// MailRBLStateRepository tracks the latest (ip, rbl) → listed/detail row.
// The eventsource owns the upsert + diffing against previous Listed; the
// REST/UI side reads ListByIP for the deliverability score badge.
type MailRBLStateRepository interface {
	GetByIPRBL(ctx context.Context, ip, rbl string) (*models.MailRBLState, error)
	Upsert(ctx context.Context, ip, rbl string, listed bool, detail *string, checkedAt time.Time) (*models.MailRBLState, error)
	ListByIP(ctx context.Context, ip string) ([]models.MailRBLState, error)
}

type mailRBLStateRepo struct{ db *gorm.DB }

func NewMailRBLStateRepository(db *gorm.DB) MailRBLStateRepository {
	return &mailRBLStateRepo{db: db}
}

func (r *mailRBLStateRepo) GetByIPRBL(ctx context.Context, ip, rbl string) (*models.MailRBLState, error) {
	var row models.MailRBLState
	if err := r.db.WithContext(ctx).
		Where("ip = ? AND rbl = ?", ip, rbl).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, translate(err)
	}
	return &row, nil
}

// Upsert is the only writer. Idempotent — handles first-insert + every
// subsequent re-probe. Returns the post-upsert row so the caller can
// detect whether `Listed` transitioned vs the prior value (the caller
// passes us the new `listed`; whatever was there before is what the
// caller fetched via GetByIPRBL pre-call).
func (r *mailRBLStateRepo) Upsert(ctx context.Context, ip, rbl string, listed bool, detail *string, checkedAt time.Time) (*models.MailRBLState, error) {
	row := &models.MailRBLState{
		ID:        ids.NewULID(),
		IP:        ip,
		RBL:       rbl,
		Listed:    listed,
		Detail:    detail,
		CheckedAt: checkedAt.UTC(),
		CreatedAt: checkedAt.UTC(),
		UpdatedAt: checkedAt.UTC(),
	}
	// ON CONFLICT(ip, rbl) UPDATE listed/detail/checked_at/updated_at.
	// id + created_at survive the first insert; subsequent upserts only
	// touch the mutating fields.
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "ip"}, {Name: "rbl"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"listed", "detail", "checked_at", "updated_at",
		}),
	}).Create(row).Error; err != nil {
		return nil, translate(err)
	}
	// Re-read so the caller sees the post-conflict row (its original
	// ID/CreatedAt on a re-upsert, not the throwaway ULID we passed).
	return r.GetByIPRBL(ctx, ip, rbl)
}

func (r *mailRBLStateRepo) ListByIP(ctx context.Context, ip string) ([]models.MailRBLState, error) {
	var rows []models.MailRBLState
	if err := r.db.WithContext(ctx).
		Where("ip = ?", ip).
		Order("rbl ASC").
		Find(&rows).Error; err != nil {
		return nil, translate(err)
	}
	return rows, nil
}
