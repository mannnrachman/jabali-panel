package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// MigrationJobRepository persists migration_jobs + migration_stages.
// Step 1 ships the wire surface; importer code (Steps 3-7) calls
// these to record progress as each stage executes under a transient
// systemd unit.
type MigrationJobRepository interface {
	Create(ctx context.Context, row *models.MigrationJob) error
	FindByID(ctx context.Context, id string) (*models.MigrationJob, error)
	// FindBySource looks up the resume target. Returns ErrNotFound
	// when no prior attempt exists for this tuple.
	FindBySource(ctx context.Context, sourceKind, sourceHost, sourceUser string) (*models.MigrationJob, error)
	List(ctx context.Context, page, pageSize int) ([]models.MigrationJob, int64, error)
	UpdateState(ctx context.Context, id, state string, lastError *string) error
	UpdateManifest(ctx context.Context, id, manifestJSON string) error
	UpdateTargetUser(ctx context.Context, id, targetUserID string) error
	Delete(ctx context.Context, id string) error

	// Stages
	CreateStage(ctx context.Context, row *models.MigrationStage) error
	ListStages(ctx context.Context, jobID string) ([]models.MigrationStage, error)
	UpdateStage(ctx context.Context, id, state string, bytesProcessed int64, lastError *string) error
}

type migrationJobRepo struct{ db *gorm.DB }

func NewMigrationJobRepository(db *gorm.DB) MigrationJobRepository {
	return &migrationJobRepo{db: db}
}

func (r *migrationJobRepo) Create(ctx context.Context, row *models.MigrationJob) error {
	now := time.Now().UTC()
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = now
	}
	if row.StartedAt.IsZero() {
		row.StartedAt = now
	}
	if row.State == "" {
		row.State = models.MigrationStatePending
	}
	return r.db.WithContext(ctx).Create(row).Error
}

func (r *migrationJobRepo) FindByID(ctx context.Context, id string) (*models.MigrationJob, error) {
	var row models.MigrationJob
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *migrationJobRepo) FindBySource(ctx context.Context, sourceKind, sourceHost, sourceUser string) (*models.MigrationJob, error) {
	var row models.MigrationJob
	err := r.db.WithContext(ctx).
		Where("source_kind = ? AND source_host = ? AND source_user = ?", sourceKind, sourceHost, sourceUser).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *migrationJobRepo) List(ctx context.Context, page, pageSize int) ([]models.MigrationJob, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	var rows []models.MigrationJob
	var total int64
	if err := r.db.WithContext(ctx).Model(&models.MigrationJob{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := r.db.WithContext(ctx).
		Order("started_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *migrationJobRepo) UpdateState(ctx context.Context, id, state string, lastError *string) error {
	patch := map[string]any{
		"state":      state,
		"updated_at": time.Now().UTC(),
		"last_error": lastError,
	}
	// Stamp ended_at on terminal states so the UI can render duration.
	switch state {
	case models.MigrationStateDone, models.MigrationStateFailed, models.MigrationStateCancelled:
		patch["ended_at"] = time.Now().UTC()
	}
	res := r.db.WithContext(ctx).Model(&models.MigrationJob{}).
		Where("id = ?", id).Updates(patch)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *migrationJobRepo) UpdateManifest(ctx context.Context, id, manifestJSON string) error {
	res := r.db.WithContext(ctx).Model(&models.MigrationJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"manifest_json": manifestJSON,
			"updated_at":    time.Now().UTC(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *migrationJobRepo) UpdateTargetUser(ctx context.Context, id, targetUserID string) error {
	res := r.db.WithContext(ctx).Model(&models.MigrationJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"target_user_id": targetUserID,
			"updated_at":     time.Now().UTC(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *migrationJobRepo) Delete(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Delete(&models.MigrationJob{}, "id = ?", id)
	if err := res.Error; err != nil {
		return err
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *migrationJobRepo) CreateStage(ctx context.Context, row *models.MigrationStage) error {
	now := time.Now().UTC()
	// Generate ULID when caller didn't supply one. Pre-fix bug: the
	// runner constructs *MigrationStage without setting ID + relied
	// on this helper to mint one; without that, gorm.Create insert
	// hit 'Duplicate entry "" for key PRIMARY' on the second stage
	// row of any job.
	if row.ID == "" {
		row.ID = ids.NewULID()
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = now
	}
	if row.State == "" {
		row.State = "pending"
	}
	return r.db.WithContext(ctx).Create(row).Error
}

func (r *migrationJobRepo) ListStages(ctx context.Context, jobID string) ([]models.MigrationStage, error) {
	var rows []models.MigrationStage
	err := r.db.WithContext(ctx).
		Where("job_id = ?", jobID).
		Order("created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *migrationJobRepo) UpdateStage(ctx context.Context, id, state string, bytesProcessed int64, lastError *string) error {
	now := time.Now().UTC()
	patch := map[string]any{
		"state":           state,
		"bytes_processed": bytesProcessed,
		"updated_at":      now,
		"last_error":      lastError,
	}
	switch state {
	case "running":
		patch["started_at"] = now
	case "done", "failed":
		patch["ended_at"] = now
	}
	res := r.db.WithContext(ctx).Model(&models.MigrationStage{}).
		Where("id = ?", id).Updates(patch)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
