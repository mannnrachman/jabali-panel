package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// EmailAutoresponderRepository defines data access for autoresponse/vacation messages.
// Stalwart integration: JMAP VacationResponse (RFC 8621 §8).
// Jabali is truth; reconciler converges to Stalwart (ADR-0051).
type EmailAutoresponderRepository interface {
	FindByMailboxID(ctx context.Context, mailboxID string) (*models.EmailAutoresponder, error)
	Update(ctx context.Context, autoresponder *models.EmailAutoresponder) error
	Delete(ctx context.Context, mailboxID string) error
}

type emailAutoresponderRepo struct {
	db *gorm.DB
}

// NewEmailAutoresponderRepository returns the GORM-backed impl.
func NewEmailAutoresponderRepository(db *gorm.DB) EmailAutoresponderRepository {
	return &emailAutoresponderRepo{db: db}
}

func (r *emailAutoresponderRepo) FindByMailboxID(ctx context.Context, mailboxID string) (*models.EmailAutoresponder, error) {
	var ar models.EmailAutoresponder
	if err := r.db.WithContext(ctx).Where("mailbox_id = ?", mailboxID).First(&ar).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &ar, nil
}

func (r *emailAutoresponderRepo) Update(ctx context.Context, autoresponder *models.EmailAutoresponder) error {
	autoresponder.UpdatedAt = time.Now().UTC()
	// Upsert by PK (mailbox_id).
	return r.db.WithContext(ctx).Save(autoresponder).Error
}

func (r *emailAutoresponderRepo) Delete(ctx context.Context, mailboxID string) error {
	res := r.db.WithContext(ctx).Delete(&models.EmailAutoresponder{}, "mailbox_id = ?", mailboxID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
