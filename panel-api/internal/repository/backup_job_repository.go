// Package repository — BackupJobRepository owns the backup_jobs table.
// Step 1 lands minimal CRUD: Create on schedule, Get for status reads,
// MarkStatus for state transitions, ListForUser for the UI list page.
// Step 6 wires the orchestrator on top; Step 8 wires the REST handlers.
//
// Schema: migration 000084 (M30 / ADR-0075).
package repository

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// BackupJobRepository is the narrow interface every consumer (orchestrator,
// REST handler, retention timer) depends on. New methods land here as the
// surface grows; do not widen ListForUser into a generic ListOptions until
// a real second caller appears.
type BackupJobRepository interface {
	Create(ctx context.Context, job *models.BackupJob) error
	Get(ctx context.Context, id string) (*models.BackupJob, error)
	ListForUser(ctx context.Context, userID string, limit, offset int) ([]models.BackupJob, int64, error)
	ListAll(ctx context.Context, limit, offset int) ([]models.BackupJob, int64, error)
	MarkStarted(ctx context.Context, id string) error
	MarkFinished(ctx context.Context, id, status string, snapshotID, parentSnapshot string, bytesAdded, bytesTotal uint64, manifest, warnings json.RawMessage, errText string) error

	// Queue helpers — used by the in-process dispatcher (M30.1
	// follow-up). CountByStatus + ListQueuedOldest let it cap
	// dispatches at server_settings.backup_max_concurrent_jobs.
	CountByStatus(ctx context.Context, status string) (int64, error)
	ListQueuedOldest(ctx context.Context, limit int) ([]models.BackupJob, error)
}

type backupJobRepo struct{ db *gorm.DB }

// NewBackupJobRepository returns a GORM-backed BackupJobRepository.
func NewBackupJobRepository(db *gorm.DB) BackupJobRepository {
	return &backupJobRepo{db: db}
}

func (r *backupJobRepo) Create(ctx context.Context, job *models.BackupJob) error {
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	if job.Status == "" {
		job.Status = models.BackupJobStatusQueued
	}
	if err := r.db.WithContext(ctx).Create(job).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *backupJobRepo) Get(ctx context.Context, id string) (*models.BackupJob, error) {
	var out models.BackupJob
	if err := r.db.WithContext(ctx).First(&out, "id = ?", id).Error; err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

func (r *backupJobRepo) ListForUser(ctx context.Context, userID string, limit, offset int) ([]models.BackupJob, int64, error) {
	return r.list(ctx, "user_id = ?", []any{userID}, limit, offset)
}

func (r *backupJobRepo) ListAll(ctx context.Context, limit, offset int) ([]models.BackupJob, int64, error) {
	return r.list(ctx, "", nil, limit, offset)
}

func (r *backupJobRepo) list(ctx context.Context, where string, args []any, limit, offset int) ([]models.BackupJob, int64, error) {
	var (
		out   []models.BackupJob
		total int64
	)
	q := r.db.WithContext(ctx).Model(&models.BackupJob{})
	if where != "" {
		q = q.Where(where, args...)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, translate(err)
	}
	if limit <= 0 {
		limit = 50
	}
	if err := q.Order("created_at DESC").
		Limit(limit).Offset(offset).
		Find(&out).Error; err != nil {
		return nil, 0, translate(err)
	}
	return out, total, nil
}

func (r *backupJobRepo) CountByStatus(ctx context.Context, status string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).
		Model(&models.BackupJob{}).
		Where("status = ?", status).
		Count(&n).Error
	if err != nil {
		return 0, translate(err)
	}
	return n, nil
}

func (r *backupJobRepo) ListQueuedOldest(ctx context.Context, limit int) ([]models.BackupJob, error) {
	if limit <= 0 {
		limit = 10
	}
	var out []models.BackupJob
	err := r.db.WithContext(ctx).
		Where("status = ?", models.BackupJobStatusQueued).
		Order("created_at ASC").
		Limit(limit).
		Find(&out).Error
	if err != nil {
		return nil, translate(err)
	}
	return out, nil
}

func (r *backupJobRepo) MarkStarted(ctx context.Context, id string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&models.BackupJob{}).
		Where("id = ? AND status = ?", id, models.BackupJobStatusQueued).
		Updates(map[string]any{
			"status":     models.BackupJobStatusRunning,
			"started_at": now,
		})
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkFinished closes a job. Status must be one of succeeded/partial/
// failed/cancelled; callers are expected to pre-validate. Manifest and
// warnings are pass-through JSON so the worker can serialize whatever
// shape ADR-0075 / Step 2 finalizes.
func (r *backupJobRepo) MarkFinished(
	ctx context.Context,
	id, status string,
	snapshotID, parentSnapshot string,
	bytesAdded, bytesTotal uint64,
	manifest, warnings json.RawMessage,
	errText string,
) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"status":          status,
		"finished_at":     now,
		"snapshot_id":     snapshotID,
		"parent_snapshot": parentSnapshot,
		"bytes_added":     bytesAdded,
		"bytes_total":     bytesTotal,
	}
	if len(manifest) > 0 {
		updates["manifest_json"] = manifest
	}
	if len(warnings) > 0 {
		updates["warnings_json"] = warnings
	}
	if errText != "" {
		updates["error_text"] = errText
	}
	res := r.db.WithContext(ctx).
		Model(&models.BackupJob{}).
		Where("id = ?", id).
		Updates(updates)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
