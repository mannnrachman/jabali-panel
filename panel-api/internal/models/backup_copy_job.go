// M30.1 async restic-copy queue (ADR-0078). Schema in migration 000090.
// One row per (backup_job, destination); enqueued after the local
// backup succeeds; processed by the copy worker which spawns
// systemd-run transient units to run `restic copy`.
package models

import "time"

const (
	BackupCopyJobStatusQueued    = "queued"
	BackupCopyJobStatusRunning   = "running"
	BackupCopyJobStatusSucceeded = "succeeded"
	BackupCopyJobStatusFailed    = "failed"
	BackupCopyJobStatusCancelled = "cancelled"
)

// BackupCopyJobMaxAttempts is the retry ceiling enforced by the worker.
// After this many `failed` transitions the job stays failed and an
// M14 notification fires (Wave B+).
const BackupCopyJobMaxAttempts = 3

type BackupCopyJob struct {
	ID            string     `gorm:"type:char(26);primaryKey" json:"id"`
	BackupJobID   string     `gorm:"type:char(26);not null;index:idx_bcj_backup" json:"backup_job_id"`
	DestinationID string     `gorm:"type:char(26);not null" json:"destination_id"`
	Status        string     `gorm:"type:enum('queued','running','succeeded','failed','cancelled');not null;default:'queued'" json:"status"`
	SystemdUnit   string     `gorm:"type:varchar(128);not null;default:''" json:"systemd_unit"`
	RetryCount    int        `gorm:"type:int;not null;default:0" json:"retry_count"`
	NextAttemptAt *time.Time `gorm:"type:datetime(6)" json:"next_attempt_at,omitempty"`
	StartedAt     *time.Time `gorm:"type:datetime(6)" json:"started_at,omitempty"`
	FinishedAt    *time.Time `gorm:"type:datetime(6)" json:"finished_at,omitempty"`
	BytesCopied   *uint64    `gorm:"type:bigint unsigned" json:"bytes_copied,omitempty"`
	ErrorText     string     `gorm:"type:text" json:"error_text,omitempty"`
	CreatedAt     time.Time  `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (BackupCopyJob) TableName() string { return "backup_copy_jobs" }
