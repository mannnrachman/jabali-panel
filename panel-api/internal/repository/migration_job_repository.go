package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
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
	ClearTargetUser(ctx context.Context, id string) error
	UpdateSourceUser(ctx context.Context, id, sourceUser string) error
	// PatchDraft updates source-host/user + target-user-id on a row
	// still in state='draft'. ADR-0095 decision 5. Returns ErrNotFound
	// if the row is missing OR in any non-draft state — callers map
	// that to 409. Pass nil for any field to skip it.
	PatchDraft(ctx context.Context, id string, sourceHost, sourceUser, targetUserID *string) error
	// ListByBatch returns every job sharing a batch_id (ADR-0095
	// decision 3 — bulk-WHM). Empty result on unknown batch.
	ListByBatch(ctx context.Context, batchID string) ([]models.MigrationJob, error)
	// CancelDraftsOlderThan deletes draft rows whose updated_at is
	// older than the cutoff. Called by the secrets reaper timer.
	CancelDraftsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
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
	// M35.8: collision check matches only ACTIVELY-OWNED rows.
	// - drafts are wizard-internal scratchpads, hidden from the UI
	// - done / failed / cancelled rows are terminal; the source is
	//   free again from the operator's perspective
	// Without this, an operator would see "existing draft owns this
	// source" pointing at a row the UI doesn't even surface, with
	// no way to switch / dismiss. Reaper cleans drafts >24h; terminal
	// rows persist for audit but don't block reruns.
	err := r.db.WithContext(ctx).
		Where("source_kind = ? AND source_host = ? AND source_user = ? AND state NOT IN ?",
			sourceKind, sourceHost, sourceUser,
			[]string{
				models.MigrationStateDraft,
				models.MigrationStateDone,
				models.MigrationStateFailed,
				models.MigrationStateCancelled,
			}).
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

// UpdateSourceUser overwrites migration_jobs.source_user for the
// given id. Used by DA analyze auto-pivot: when the operator typed
// `root` / `admin` (SSH user) but the actual hosting account on the
// source is something else, analyze resolves the true account name
// and persists it so downstream backup/restore stages target it.
func (r *migrationJobRepo) UpdateSourceUser(ctx context.Context, id, sourceUser string) error {
	res := r.db.WithContext(ctx).Model(&models.MigrationJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"source_user": sourceUser,
			"updated_at":  time.Now().UTC(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearTargetUser sets migration_jobs.target_user_id to NULL.
// Used by the DA preflight pivot to drop a stale FK pointing at a
// panel user named after the SSH principal (root/admin) so the
// downstream auto-create path provisions a fresh user matching
// the pivoted hosting account.
func (r *migrationJobRepo) ClearTargetUser(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Model(&models.MigrationJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"target_user_id": gorm.Expr("NULL"),
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

// PatchDraft updates draft-only mutable fields. Caller passes nil to skip.
func (r *migrationJobRepo) PatchDraft(ctx context.Context, id string, sourceHost, sourceUser, targetUserID *string) error {
	updates := map[string]any{"updated_at": time.Now().UTC()}
	if sourceHost != nil {
		updates["source_host"] = *sourceHost
	}
	if sourceUser != nil {
		updates["source_user"] = *sourceUser
	}
	if targetUserID != nil {
		updates["target_user_id"] = *targetUserID
	}
	res := r.db.WithContext(ctx).
		Model(&models.MigrationJob{}).
		Where("id = ? AND state = ?", id, models.MigrationStateDraft).
		Updates(updates)
	if res.Error != nil {
		// Map 1062 (unique-key violation on (host, user, kind))
		// to repository.ErrConflict so the handler returns 409
		// instead of leaking the raw "Duplicate entry" 500.
		var my *mysql.MySQLError
		if errors.As(res.Error, &my) && my.Number == 1062 {
			return ErrConflict
		}
		if strings.Contains(res.Error.Error(), "Duplicate entry") {
			return ErrConflict
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByBatch returns all jobs with batch_id = ?.
func (r *migrationJobRepo) ListByBatch(ctx context.Context, batchID string) ([]models.MigrationJob, error) {
	var rows []models.MigrationJob
	res := r.db.WithContext(ctx).
		Where("batch_id = ?", batchID).
		Order("created_at ASC").
		Find(&rows)
	return rows, res.Error
}

// CancelDraftsOlderThan hard-deletes draft rows that haven't moved in
// `cutoff` (typically now-24h). Returns rows affected.
func (r *migrationJobRepo) CancelDraftsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("state = ? AND updated_at < ?", models.MigrationStateDraft, cutoff).
		Delete(&models.MigrationJob{})
	return res.RowsAffected, res.Error
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
