package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// DMARCAggregateRepository is append-only: rows are written by the
// Wave-6 ingest source and read by the per-domain dashboard / the
// deliverability score widget (Wave 9). PruneOlderThan implements the
// 90-day retention noted on mig 000139.
type DMARCAggregateRepository interface {
	// InsertMany bulk-inserts rows. Each row gets a fresh ULID + the
	// ingest-time `CreatedAt`. Empty `rows` is a no-op (the ingest
	// loop calls this once per report regardless of `<record>` count).
	InsertMany(ctx context.Context, rows []models.DMARCAggregate) (int, error)

	// ExistsForReport short-circuits ingest when a report (identified
	// by reporter + window) has already been imported. RUA messages
	// can be re-delivered (server retries, operator backfill) so the
	// ingest source MUST gate on this.
	ExistsForReport(ctx context.Context, reporter string, windowStart, windowEnd time.Time) (bool, error)

	ListByDomainSince(ctx context.Context, domain string, since time.Time) ([]models.DMARCAggregate, error)

	// PruneOlderThan deletes rows with window_end < cutoff. Returns the
	// number removed. Called by the retention reconciler.
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

type dmarcRepo struct{ db *gorm.DB }

func NewDMARCAggregateRepository(db *gorm.DB) DMARCAggregateRepository {
	return &dmarcRepo{db: db}
}

func (r *dmarcRepo) InsertMany(ctx context.Context, rows []models.DMARCAggregate) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	now := time.Now().UTC()
	for i := range rows {
		if rows[i].ID == "" {
			rows[i].ID = ids.NewULID()
		}
		if rows[i].CreatedAt.IsZero() {
			rows[i].CreatedAt = now
		}
	}
	if err := r.db.WithContext(ctx).Create(&rows).Error; err != nil {
		return 0, translate(err)
	}
	return len(rows), nil
}

func (r *dmarcRepo) ExistsForReport(ctx context.Context, reporter string, windowStart, windowEnd time.Time) (bool, error) {
	var n int64
	if err := r.db.WithContext(ctx).
		Model(&models.DMARCAggregate{}).
		Where("reporter = ? AND window_start = ? AND window_end = ?",
			reporter, windowStart.UTC(), windowEnd.UTC()).
		Limit(1).
		Count(&n).Error; err != nil {
		return false, translate(err)
	}
	return n > 0, nil
}

func (r *dmarcRepo) ListByDomainSince(ctx context.Context, domain string, since time.Time) ([]models.DMARCAggregate, error) {
	var rows []models.DMARCAggregate
	if err := r.db.WithContext(ctx).
		Where("domain = ? AND window_end >= ?", domain, since.UTC()).
		Order("window_end DESC").
		Find(&rows).Error; err != nil {
		return nil, translate(err)
	}
	return rows, nil
}

func (r *dmarcRepo) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("window_end < ?", cutoff.UTC()).
		Delete(&models.DMARCAggregate{})
	if res.Error != nil {
		return 0, translate(res.Error)
	}
	return res.RowsAffected, nil
}
