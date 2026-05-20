package models

import "time"

// MailRBLState mirrors `mail_rbl_state` (mig 000139, M47 Wave 5).
// One row per (ip, rbl) — the latest probe result jabali holds for
// that pair. Transitions of `Listed` drive M14 events
// (mail.rbl.listed / mail.rbl.cleared) so the operator learns their
// outbound IP is blacklisted before customers do.
type MailRBLState struct {
	ID        string    `gorm:"column:id;type:char(26);primaryKey" json:"id"`
	IP        string    `gorm:"column:ip;type:varchar(45);not null;uniqueIndex:uq_rbl,priority:1" json:"ip"`
	RBL       string    `gorm:"column:rbl;type:varchar(64);not null;uniqueIndex:uq_rbl,priority:2" json:"rbl"`
	Listed    bool      `gorm:"column:listed;type:tinyint(1);not null;default:0" json:"listed"`
	Detail    *string   `gorm:"column:detail;type:text" json:"detail,omitempty"`
	CheckedAt time.Time `gorm:"column:checked_at;type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"checked_at"`
	CreatedAt time.Time `gorm:"column:created_at;type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"updated_at"`
}

func (MailRBLState) TableName() string { return "mail_rbl_state" }
