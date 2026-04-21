package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// MailboxRepository defines data access for per-domain mailboxes
// (ADR-0042). The SqlDirectory under Stalwart reads this table on
// every auth; panel-API is the only writer.
//
// EmailCached is maintained by BEFORE INSERT/UPDATE triggers from
// migration 000054 — we never set it here directly.
type MailboxRepository interface {
	FindByID(ctx context.Context, id string) (*models.Mailbox, error)
	FindByEmail(ctx context.Context, email string) (*models.Mailbox, error)
	ListByDomainID(ctx context.Context, domainID string, opts ListOptions) ([]models.Mailbox, int64, error)
	CountByDomainID(ctx context.Context, domainID string) (int64, error)
	Create(ctx context.Context, mb *models.Mailbox) error
	Delete(ctx context.Context, id string) error
	UpdatePasswordHash(ctx context.Context, id string, hash string) error
	UpdateQuota(ctx context.Context, id string, quotaBytes uint64) error
	UpdateUsage(ctx context.Context, id string, usageBytes uint64, at time.Time) error
	ExistsByDomainAndLocalPart(ctx context.Context, domainID, localPart string) (bool, error)
}

type mailboxRepo struct{ db *gorm.DB }

func NewMailboxRepository(db *gorm.DB) MailboxRepository {
	return &mailboxRepo{db: db}
}

func (r *mailboxRepo) FindByID(ctx context.Context, id string) (*models.Mailbox, error) {
	var mb models.Mailbox
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&mb).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &mb, nil
}

func (r *mailboxRepo) FindByEmail(ctx context.Context, email string) (*models.Mailbox, error) {
	var mb models.Mailbox
	if err := r.db.WithContext(ctx).Where("email_cached = ?", email).First(&mb).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &mb, nil
}

// mailboxListCols — free-text search matches local_part and the cached
// full address so a query for "alice" or "alice@example.com" both hit.
var mailboxListCols = ListCols{
	Search:      []string{"local_part", "email_cached"},
	Sort:        []string{"local_part", "email_cached", "created_at", "quota_bytes", "last_usage_bytes"},
	DefaultSort: "created_at",
}

func (r *mailboxRepo) ListByDomainID(ctx context.Context, domainID string, opts ListOptions) ([]models.Mailbox, int64, error) {
	var (
		rows  []models.Mailbox
		total int64
	)
	base := r.db.WithContext(ctx).Model(&models.Mailbox{}).Where("domain_id = ?", domainID)

	countQ := applyListOptions(base.Session(&gorm.Session{}), ListOptions{Search: opts.Search}, mailboxListCols)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if opts.Sort == "" && opts.Order == "" {
		opts.Order = "desc"
	}
	q := applyListOptions(base.Session(&gorm.Session{}), opts, mailboxListCols)
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *mailboxRepo) CountByDomainID(ctx context.Context, domainID string) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&models.Mailbox{}).Where("domain_id = ?", domainID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// Create inserts a mailbox row. Caller must pre-populate:
//   - ID (ULID)
//   - DomainID
//   - LocalPart
//   - PasswordHash (bcrypt)
//   - QuotaBytes (or rely on the default via the column DEFAULT)
//   - CreatedAt / UpdatedAt
//
// EmailCached does NOT need to be set; the BEFORE INSERT trigger
// computes it as CONCAT(local_part, '@', domain.name). Setting it
// from Go is harmless (the trigger overwrites it anyway), but the
// caller should not RELY on that value.
func (r *mailboxRepo) Create(ctx context.Context, mb *models.Mailbox) error {
	return r.db.WithContext(ctx).Create(mb).Error
}

func (r *mailboxRepo) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&models.Mailbox{}).Error
}

func (r *mailboxRepo) UpdatePasswordHash(ctx context.Context, id string, hash string) error {
	return r.db.WithContext(ctx).Model(&models.Mailbox{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"password_hash": hash,
			"updated_at":    time.Now().UTC(),
		}).Error
}

func (r *mailboxRepo) UpdateQuota(ctx context.Context, id string, quotaBytes uint64) error {
	return r.db.WithContext(ctx).Model(&models.Mailbox{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"quota_bytes": quotaBytes,
			"updated_at":  time.Now().UTC(),
		}).Error
}

// UpdateUsage writes back the last observed usage bytes and sample
// time from the reconciler's mailbox.usage probe. Kept separate from
// UpdateQuota so we can lock the reconciler down with a narrower
// grant if we ever split it out.
func (r *mailboxRepo) UpdateUsage(ctx context.Context, id string, usageBytes uint64, at time.Time) error {
	return r.db.WithContext(ctx).Model(&models.Mailbox{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"last_usage_bytes": usageBytes,
			"last_usage_at":    at.UTC(),
			// updated_at intentionally NOT touched here — usage
			// writebacks shouldn't mark the row as user-edited.
		}).Error
}

func (r *mailboxRepo) ExistsByDomainAndLocalPart(ctx context.Context, domainID, localPart string) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&models.Mailbox{}).
		Where("domain_id = ? AND local_part = ?", domainID, localPart).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}
