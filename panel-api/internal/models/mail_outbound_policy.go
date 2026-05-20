package models

import "time"

// MailOutboundPolicy is one outbound rate-cap row in the
// mail_outbound_policy table (mig 000139 + mig 000144).
//
// scope ∈ {user, domain, global}. scope_ref is the matching ULID
// (users.id for scope=user; domains.id for scope=domain; NULL for
// scope=global — a single server-wide cap).
//
// max_per_hour / max_per_day = 0 means unlimited. The reconciler
// (Wave 3) converges each enabled row into a Stalwart
// MtaOutboundThrottle object via internal/stalwartadmin; the assigned
// upstream id lives in stalwart_id so subsequent updates target the
// right object.
type MailOutboundPolicy struct {
	ID            string     `gorm:"column:id;type:char(26);primaryKey" json:"id"`
	Scope         string     `gorm:"column:scope;type:varchar(16);not null" json:"scope"`
	ScopeRef      *string    `gorm:"column:scope_ref;type:char(26)" json:"scope_ref,omitempty"`
	MaxPerHour    uint       `gorm:"column:max_per_hour;type:int unsigned;not null;default:0" json:"max_per_hour"`
	MaxPerDay     uint       `gorm:"column:max_per_day;type:int unsigned;not null;default:0" json:"max_per_day"`
	Enabled       bool       `gorm:"column:enabled;type:tinyint(1);not null;default:1" json:"enabled"`
	StalwartID    string     `gorm:"column:stalwart_id;type:varchar(64);not null;default:''" json:"stalwart_id"`
	// StalwartIDDaily tracks the SECOND Stalwart throttle when both
	// hourly + daily caps are set (mig 000146 / ADR-0112 v3). Empty
	// when only one rate window is active.
	StalwartIDDaily string `gorm:"column:stalwart_id_daily;type:varchar(64);not null;default:''" json:"stalwart_id_daily"`
	LastAppliedAt *time.Time `gorm:"column:last_applied_at;type:datetime(6)" json:"last_applied_at,omitempty"`
	LastError     *string    `gorm:"column:last_error;type:text" json:"last_error,omitempty"`
	CreatedAt     time.Time  `gorm:"column:created_at;type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"updated_at"`
}

func (MailOutboundPolicy) TableName() string { return "mail_outbound_policy" }

// Scope constants — keep aligned with the migration's VARCHAR check
// (none enforced at DB level; the repo + handler reject anything else).
const (
	OutboundScopeUser   = "user"
	OutboundScopeDomain = "domain"
	OutboundScopeGlobal = "global"
)
