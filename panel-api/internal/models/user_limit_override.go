package models

import "time"

// UserLimitOverride carries per-user limit overrides on top of a user's
// hosting package. Every field is nullable with explicit semantics —
// see the resolver in internal/limits/resolve.go:
//
//   NULL  → inherit from package
//   0     → unlimited (override to unbounded)
//   N > 0 → cap at N, overriding whatever the package says
//
// A dedicated table (vs a JSON blob on users) gives us auditable
// updated_at, easy cascade delete, and lets the reconciler LEFT JOIN
// when computing the effective limits table for an entire host.
type UserLimitOverride struct {
	UserID string `gorm:"type:char(26);primaryKey" json:"user_id"`

	DiskQuotaMB     *uint32 `gorm:"type:int unsigned" json:"disk_quota_mb,omitempty"`
	CPUQuotaPercent *uint32 `gorm:"type:int unsigned" json:"cpu_quota_percent,omitempty"`
	MemoryLimitMB   *uint32 `gorm:"type:int unsigned" json:"memory_limit_mb,omitempty"`
	IOReadMbps      *uint32 `gorm:"type:int unsigned" json:"io_read_mbps,omitempty"`
	IOWriteMbps     *uint32 `gorm:"type:int unsigned" json:"io_write_mbps,omitempty"`
	MaxTasks        *uint32 `gorm:"type:int unsigned" json:"max_tasks,omitempty"`

	UpdatedAt time.Time `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (UserLimitOverride) TableName() string { return "user_limit_overrides" }
