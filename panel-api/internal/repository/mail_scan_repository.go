package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// MailScanStateRepository owns the per-mailbox cursor table for the
// M33.2 async mail YARA scanner. (account_id, mailbox_id) is the
// composite key.
type MailScanStateRepository interface {
	Get(ctx context.Context, accountID, mailboxID string) (*models.MailScanState, error)
	Upsert(ctx context.Context, s *models.MailScanState) error
	// PickStaleForTick returns up to limit rows ordered by scanned_at
	// ASC (oldest first) so a per-tick budget converges across cycles
	// rather than starving newcomers. Pass an empty list when no rows
	// exist yet — the orchestrator initialises rows lazily on first
	// pass over a previously-unseen (account, mailbox) pair.
	PickStaleForTick(ctx context.Context, limit int) ([]models.MailScanState, error)
}

type mailScanStateRepo struct{ db *gorm.DB }

func NewMailScanStateRepository(db *gorm.DB) MailScanStateRepository {
	return &mailScanStateRepo{db: db}
}

func (r *mailScanStateRepo) Get(ctx context.Context, accountID, mailboxID string) (*models.MailScanState, error) {
	var s models.MailScanState
	err := r.db.WithContext(ctx).
		Where("account_id = ? AND mailbox_id = ?", accountID, mailboxID).
		First(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, translate(err)
	}
	return &s, nil
}

func (r *mailScanStateRepo) Upsert(ctx context.Context, s *models.MailScanState) error {
	if s.ScannedAt.IsZero() {
		s.ScannedAt = time.Now().UTC()
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "account_id"}, {Name: "mailbox_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"last_email_id", "last_received_at",
			"scanned_count", "hit_count", "failure_count",
			"scanned_at", "quarantine_mailbox", "quarantine_mailbox_verified",
		}),
	}).Create(s).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *mailScanStateRepo) PickStaleForTick(ctx context.Context, limit int) ([]models.MailScanState, error) {
	if limit <= 0 {
		limit = 200
	}
	var rows []models.MailScanState
	if err := r.db.WithContext(ctx).
		Order("scanned_at ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, translate(err)
	}
	return rows, nil
}

// MailScanFailureRepository is the DLQ for tick failures. Bounded
// retention via app-side purge — keep the most-recent 10k rows.
type MailScanFailureRepository interface {
	Create(ctx context.Context, f *models.MailScanFailure) error
	Recent(ctx context.Context, limit int) ([]models.MailScanFailure, error)
	PurgeOlderThan(ctx context.Context, keep int) (int64, error)
}

type mailScanFailureRepo struct{ db *gorm.DB }

func NewMailScanFailureRepository(db *gorm.DB) MailScanFailureRepository {
	return &mailScanFailureRepo{db: db}
}

func (r *mailScanFailureRepo) Create(ctx context.Context, f *models.MailScanFailure) error {
	if f.AttemptedAt.IsZero() {
		f.AttemptedAt = time.Now().UTC()
	}
	if err := r.db.WithContext(ctx).Create(f).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *mailScanFailureRepo) Recent(ctx context.Context, limit int) ([]models.MailScanFailure, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var rows []models.MailScanFailure
	if err := r.db.WithContext(ctx).
		Order("attempted_at DESC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, translate(err)
	}
	return rows, nil
}

// PurgeOlderThan keeps the newest `keep` rows + drops the rest. Returns
// the count deleted. Called from the tick orchestrator once a day.
func (r *mailScanFailureRepo) PurgeOlderThan(ctx context.Context, keep int) (int64, error) {
	if keep <= 0 {
		keep = 10000
	}
	// Delete rows whose attempted_at is older than the row at offset `keep`.
	var cutoff time.Time
	row := r.db.WithContext(ctx).Model(&models.MailScanFailure{}).
		Order("attempted_at DESC").
		Offset(keep).
		Limit(1).
		Pluck("attempted_at", &cutoff)
	if row.Error != nil {
		return 0, translate(row.Error)
	}
	if cutoff.IsZero() {
		return 0, nil // table has fewer rows than keep
	}
	res := r.db.WithContext(ctx).
		Where("attempted_at < ?", cutoff).
		Delete(&models.MailScanFailure{})
	if res.Error != nil {
		return 0, translate(res.Error)
	}
	return res.RowsAffected, nil
}
