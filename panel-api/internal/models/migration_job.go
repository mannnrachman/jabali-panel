package models

import "time"

// MigrationSourceKind enumerates supported source-panel kinds. Wire
// constants live as string literals so a new importer (e.g. `plesk`)
// only needs a switch arm in internal/migrate, no schema migration.
const (
	MigrationSourceCpanel       = "cpanel"
	MigrationSourceDirectAdmin  = "directadmin"
	MigrationSourceHestia       = "hestiacp"
	MigrationSourceWHMpkgacct   = "whm_pkgacct"
	MigrationSourceIMAPOnly     = "imap_only"
)

// MigrationState is the per-job lifecycle. Stage transitions are
// pinned in internal/migrate/stage.go (Step 2 wave gate).
const (
	MigrationStatePending     = "pending"
	MigrationStateAnalyzing   = "analyzing"
	MigrationStateFixPerms    = "fix_perms"
	MigrationStateValidating  = "validating"
	MigrationStateRestoring   = "restoring"
	MigrationStateDone        = "done"
	MigrationStateFailed      = "failed"
	MigrationStateCancelled   = "cancelled"
)

// MigrationJob is the header row for one migration attempt against
// a single (source-host, source-user, source-kind) tuple. Resume
// after a failed mid-run reuses the row — the UNIQUE on the natural
// key prevents two parallel attempts from racing on the same files.
type MigrationJob struct {
	ID            string     `gorm:"column:id;type:char(26);primaryKey" json:"id"`
	SourceKind    string     `gorm:"column:source_kind;type:varchar(32);not null;uniqueIndex:uq_migration_source,priority:3" json:"source_kind"`
	SourceHost    string     `gorm:"column:source_host;type:varchar(255);not null;uniqueIndex:uq_migration_source,priority:1" json:"source_host"`
	SourceUser    string     `gorm:"column:source_user;type:varchar(64);not null;uniqueIndex:uq_migration_source,priority:2" json:"source_user"`
	TargetUserID  *string    `gorm:"column:target_user_id;type:char(26);index:idx_migration_target_user" json:"target_user_id,omitempty"`
	State         string     `gorm:"column:state;type:varchar(32);not null;default:'pending';index:idx_migration_state" json:"state"`
	StartedAt     time.Time  `gorm:"column:started_at;type:datetime(6);not null" json:"started_at"`
	EndedAt       *time.Time `gorm:"column:ended_at;type:datetime(6)" json:"ended_at,omitempty"`
	ManifestJSON  *string    `gorm:"column:manifest_json;type:longtext" json:"manifest_json,omitempty"`
	LastError     *string    `gorm:"column:last_error;type:text" json:"last_error,omitempty"`
	CreatedAt     time.Time  `gorm:"column:created_at;type:datetime(6);not null" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;type:datetime(6);not null" json:"updated_at"`
}

func (MigrationJob) TableName() string { return "migration_jobs" }

// MigrationStage is one row per pipeline stage that runs under a
// MigrationJob. Resume scans for state in {'pending','failed'}
// ordered by created_at and re-dispatches.
type MigrationStage struct {
	ID             string     `gorm:"column:id;type:char(26);primaryKey" json:"id"`
	JobID          string     `gorm:"column:job_id;type:char(26);not null;index:idx_migration_stages_job,priority:1" json:"job_id"`
	StageName      string     `gorm:"column:stage_name;type:varchar(64);not null;index:idx_migration_stages_job,priority:2" json:"stage_name"`
	State          string     `gorm:"column:state;type:varchar(16);not null;default:'pending'" json:"state"`
	StartedAt      *time.Time `gorm:"column:started_at;type:datetime(6)" json:"started_at,omitempty"`
	EndedAt        *time.Time `gorm:"column:ended_at;type:datetime(6)" json:"ended_at,omitempty"`
	BytesProcessed int64      `gorm:"column:bytes_processed;type:bigint;not null;default:0" json:"bytes_processed"`
	LastError      *string    `gorm:"column:last_error;type:text" json:"last_error,omitempty"`
	CreatedAt      time.Time  `gorm:"column:created_at;type:datetime(6);not null" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;type:datetime(6);not null" json:"updated_at"`
}

func (MigrationStage) TableName() string { return "migration_stages" }
