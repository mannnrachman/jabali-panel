package models

import "time"

// Mailbox is a per-domain email account. Stalwart's SqlDirectory reads
// this table on every auth (ADR-0042); the panel owns the write path
// (ADR-0003) and the bcrypt hash lives in PasswordHash.
//
// EmailCached is a denormalised `local_part + '@' + domains.name`
// maintained by BEFORE INSERT/UPDATE triggers — we don't write it
// from Go code. Stalwart's queryLogin matches on EmailCached.
type Mailbox struct {
	ID             string    `gorm:"type:char(26);primaryKey" json:"id"`
	DomainID       string    `gorm:"type:char(26);not null;index:ix_mailboxes_domain" json:"domain_id"`
	LocalPart      string    `gorm:"type:varchar(64);not null" json:"local_part"`
	EmailCached    string    `gorm:"type:varchar(320);not null;uniqueIndex:ux_mailboxes_email_cached" json:"email"`
	PasswordHash   string    `gorm:"type:varchar(255);not null" json:"-"`
	QuotaBytes     uint64    `gorm:"type:bigint unsigned;not null;default:1073741824" json:"quota_bytes"`
	IsDisabled     bool      `gorm:"type:tinyint(1);not null;default:0" json:"is_disabled"`
	LastUsageBytes uint64    `gorm:"type:bigint unsigned;not null;default:0" json:"last_usage_bytes"`
	LastUsageAt    *time.Time `gorm:"type:datetime(6)" json:"last_usage_at,omitempty"`
	CreatedAt      time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (Mailbox) TableName() string { return "mailboxes" }
