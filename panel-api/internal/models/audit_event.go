package models

import (
	"encoding/json"
	"time"
)

// M49 — unified audit log (ADR-0105, migration 000137).

// Audit result + actor-kind enums. Kept as constants so the recorder
// and typed constructors (internal/audit) never stringly-type these.
const (
	AuditResultOK     = "ok"
	AuditResultDenied = "denied"
	AuditResultError  = "error"

	AuditActorUser       = "user"
	AuditActorAdmin      = "admin"
	AuditActorAutomation = "automation"
	AuditActorSystem     = "system"
	AuditActorCLI        = "cli"
)

// AuditEvent is one append-only row in the unified audit timeline.
//
// The recorder publishes a structured record to the dedicated
// jabali:audit:queue Redis stream; a single-writer chain consumer
// computes PrevHash->RowHash and persists. Rows are NEVER mutated
// after the consumer seals them — the ONE controlled exception is the
// consumer back-filling PrevHash/RowHash on rows the Redis-down DB
// fallback inserted with NULL hashes (ADR-0105), which the repository
// gates with `WHERE row_hash IS NULL` so a sealed row can't be
// rewritten. NEVER carries request bodies or secrets; Meta is
// structured context only.
//
// SubjectUserID is the keystone of the per-user /me/activity view: a
// NULL subject is structurally invisible to that scope (safe-fail,
// never cross-tenant-leaked) — see AuditEventRepository.ListBySubject.
type AuditEvent struct {
	ID            string          `gorm:"type:char(26);primaryKey" json:"id"`            // ULID
	TS            time.Time       `gorm:"type:datetime(6);not null" json:"ts"`           // event time (only timestamp; append-only)
	ActorUserID   *string         `gorm:"type:char(26)" json:"actor_user_id,omitempty"`  // users.id of who acted; NULL for system/automation/cli
	ActorKind     string          `gorm:"type:varchar(16);not null" json:"actor_kind"`   // user|admin|automation|system|cli
	SubjectUserID *string         `gorm:"type:char(26)" json:"subject_user_id,omitempty"` // whose account/resource; NULL = not user-scoped
	Action        string          `gorm:"type:varchar(128);not null" json:"action"`      // method+route template OR domain action; NEVER bodies
	TargetType    string          `gorm:"type:varchar(32);not null" json:"target_type"`  // 'domain'|'database'|'user'|'token'|...
	TargetID      string          `gorm:"type:varchar(255);not null" json:"target_id"`   // id/name; NEVER secrets
	Result        string          `gorm:"type:varchar(16);not null" json:"result"`       // ok|denied|error
	SourceIP      *string         `gorm:"type:varchar(45)" json:"source_ip,omitempty"`
	RequestID     *string         `gorm:"type:varchar(64)" json:"request_id,omitempty"`
	PrevHash      *string         `gorm:"type:char(64)" json:"prev_hash,omitempty"` // NULL = pre-chain (folded/historical/fallback-pending)
	RowHash       *string         `gorm:"type:char(64)" json:"row_hash,omitempty"`  // set by the single-writer chain consumer
	Meta          json.RawMessage `gorm:"type:json" json:"meta,omitempty"`          // structured context only; NEVER secrets/bodies

	// Denormalized for the read API ONLY — never a column (gorm:"-"),
	// never persisted, never used for scoping. The list handler batch-
	// resolves ActorUserID/SubjectUserID to a display name (username,
	// falling back to email) so the UI renders "alice" instead of a
	// raw ULID — one lookup per page, no N+1 (M13.1 convention).
	ActorName   *string `gorm:"-" json:"actor_name,omitempty"`
	SubjectName *string `gorm:"-" json:"subject_name,omitempty"`
}

func (AuditEvent) TableName() string { return "audit_events" }
