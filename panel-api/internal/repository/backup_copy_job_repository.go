// Package repository — BackupCopyJobRepository owns backup_copy_jobs.
// M30.1 / ADR-0078.
package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

type BackupCopyJobRepository interface {
	Create(ctx context.Context, j *models.BackupCopyJob) error
	Get(ctx context.Context, id string) (*models.BackupCopyJob, error)
	ListByBackupJob(ctx context.Context, backupJobID string) ([]models.BackupCopyJob, error)
	ListQueued(ctx context.Context, now time.Time, limit int) ([]models.BackupCopyJob, error)
	MarkRunning(ctx context.Context, id, systemdUnit string) error
	MarkSucceeded(ctx context.Context, id string, bytesCopied uint64) error
	MarkFailed(ctx context.Context, id, errText string, retry bool, nextAttemptAt *time.Time) error
	Cancel(ctx context.Context, id string) error
}

type backupCopyJobRepo struct{ db *gorm.DB }

func NewBackupCopyJobRepository(db *gorm.DB) BackupCopyJobRepository {
	return &backupCopyJobRepo{db: db}
}

func (r *backupCopyJobRepo) Create(ctx context.Context, j *models.BackupCopyJob) error {
	now := time.Now().UTC()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = now
	}
	if j.UpdatedAt.IsZero() {
		j.UpdatedAt = now
	}
	if j.Status == "" {
		j.Status = models.BackupCopyJobStatusQueued
	}
	if j.NextAttemptAt == nil {
		t := now
		j.NextAttemptAt = &t
	}
	if err := r.db.WithContext(ctx).Create(j).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *backupCopyJobRepo) Get(ctx context.Context, id string) (*models.BackupCopyJob, error) {
	var out models.BackupCopyJob
	if err := r.db.WithContext(ctx).First(&out, "id = ?", id).Error; err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

func (r *backupCopyJobRepo) ListByBackupJob(ctx context.Context, backupJobID string) ([]models.BackupCopyJob, error) {
	var out []models.BackupCopyJob
	if err := r.db.WithContext(ctx).
		Where("backup_job_id = ?", backupJobID).
		Order("created_at ASC").
		Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

func (r *backupCopyJobRepo) ListQueued(ctx context.Context, now time.Time, limit int) ([]models.BackupCopyJob, error) {
	if limit <= 0 {
		limit = 5
	}
	var out []models.BackupCopyJob
	if err := r.db.WithContext(ctx).
		Where("status = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)",
			models.BackupCopyJobStatusQueued, now).
		Order("next_attempt_at ASC, created_at ASC").
		Limit(limit).
		Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

func (r *backupCopyJobRepo) MarkRunning(ctx context.Context, id, systemdUnit string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&models.BackupCopyJob{}).
		Where("id = ? AND status = ?", id, models.BackupCopyJobStatusQueued).
		Updates(map[string]any{
			"status":        models.BackupCopyJobStatusRunning,
			"systemd_unit":  systemdUnit,
			"started_at":    now,
			"updated_at":    now,
		})
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *backupCopyJobRepo) MarkSucceeded(ctx context.Context, id string, bytesCopied uint64) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&models.BackupCopyJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":       models.BackupCopyJobStatusSucceeded,
			"finished_at":  now,
			"bytes_copied": bytesCopied,
			"error_text":   "",
			"updated_at":   now,
		})
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkFailed transitions the row to either `failed` (terminal) or back to
// `queued` with retry_count++ when retry is true. Caller computes
// nextAttemptAt (exponential backoff lives in the worker, not the repo).
func (r *backupCopyJobRepo) MarkFailed(ctx context.Context, id, errText string, retry bool, nextAttemptAt *time.Time) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"error_text": errText,
		"updated_at": now,
	}
	if retry {
		updates["status"] = models.BackupCopyJobStatusQueued
		updates["next_attempt_at"] = nextAttemptAt
	} else {
		updates["status"] = models.BackupCopyJobStatusFailed
		updates["finished_at"] = now
	}
	res := r.db.WithContext(ctx).
		Model(&models.BackupCopyJob{}).
		Where("id = ?", id).
		UpdateColumns(updates)
	if res.Error != nil {
		return translate(res.Error)
	}
	// Bump retry_count separately to keep the SQL deterministic.
	if retry {
		if err := r.db.WithContext(ctx).
			Model(&models.BackupCopyJob{}).
			Where("id = ?", id).
			UpdateColumn("retry_count", gorm.Expr("retry_count + 1")).Error; err != nil {
			return translate(err)
		}
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *backupCopyJobRepo) Cancel(ctx context.Context, id string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&models.BackupCopyJob{}).
		Where("id = ? AND status IN ?", id, []string{
			models.BackupCopyJobStatusQueued,
			models.BackupCopyJobStatusRunning,
		}).
		Updates(map[string]any{
			"status":      models.BackupCopyJobStatusCancelled,
			"finished_at": now,
			"updated_at":  now,
		})
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
