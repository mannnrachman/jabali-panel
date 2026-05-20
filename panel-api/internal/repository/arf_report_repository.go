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

// ARFReportRepository is append-only: rows are written by the
// mail_abuse_ingest event source and read by the deliverability
// score widget (Wave 9). PruneOlderThan implements the 90-day
// retention noted on mig 000143.
type ARFReportRepository interface {
	InsertMany(ctx context.Context, rows []models.ARFReport) (int, error)
	// ExistsForStalwartID short-circuits ingest when a report's
	// upstream id has already been imported. Drives idempotency on
	// re-runs / backfills.
	ExistsForStalwartID(ctx context.Context, stalwartID string) (bool, error)
	ListSince(ctx context.Context, since time.Time, limit int) ([]models.ARFReport, error)
	CountSince(ctx context.Context, since time.Time) (int64, error)
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	// MostRecentReceivedAt returns the highest received_at we already
	// stored, used as the cursor for the next Stalwart query. Zero
	// time when the table is empty.
	MostRecentReceivedAt(ctx context.Context) (time.Time, error)
}

type arfRepo struct{ db *gorm.DB }

func NewARFReportRepository(db *gorm.DB) ARFReportRepository {
	return &arfRepo{db: db}
}

func (r *arfRepo) InsertMany(ctx context.Context, rows []models.ARFReport) (int, error) {
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
	// Use OnConflict-do-nothing on stalwart_id so a partial-batch
	// retry doesn't blow up on the second pass.
	res := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "stalwart_id"}},
			DoNothing: true,
		}).
		Create(&rows)
	if res.Error != nil {
		return 0, translate(res.Error)
	}
	return int(res.RowsAffected), nil
}

func (r *arfRepo) ExistsForStalwartID(ctx context.Context, stalwartID string) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&models.ARFReport{}).
		Where("stalwart_id = ?", stalwartID).
		Count(&count).Error; err != nil {
		return false, translate(err)
	}
	return count > 0, nil
}

func (r *arfRepo) ListSince(ctx context.Context, since time.Time, limit int) ([]models.ARFReport, error) {
	var rows []models.ARFReport
	q := r.db.WithContext(ctx).
		Where("received_at >= ?", since).
		Order("received_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, translate(err)
	}
	return rows, nil
}

func (r *arfRepo) CountSince(ctx context.Context, since time.Time) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&models.ARFReport{}).
		Where("received_at >= ?", since).
		Count(&count).Error; err != nil {
		return 0, translate(err)
	}
	return count, nil
}

func (r *arfRepo) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("received_at < ?", cutoff).
		Delete(&models.ARFReport{})
	if res.Error != nil {
		return 0, translate(res.Error)
	}
	return res.RowsAffected, nil
}

func (r *arfRepo) MostRecentReceivedAt(ctx context.Context) (time.Time, error) {
	var row models.ARFReport
	err := r.db.WithContext(ctx).
		Select("received_at").
		Order("received_at DESC").
		Limit(1).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, translate(err)
	}
	return row.ReceivedAt, nil
}
