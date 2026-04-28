package models

import "time"

// MailScanState is the per-mailbox cursor for the M33.2 async mail YARA
// scanner. (account_id, mailbox_id) is the composite primary key
// because JMAP mailbox ids are scoped to accounts. ADR-0079.
type MailScanState struct {
	AccountID                 string     `gorm:"column:account_id;type:varchar(64);primaryKey"            json:"account_id"`
	MailboxID                 string     `gorm:"column:mailbox_id;type:varchar(64);primaryKey"            json:"mailbox_id"`
	LastEmailID               *string    `gorm:"column:last_email_id;type:varchar(64)"                    json:"last_email_id,omitempty"`
	LastReceivedAt            *time.Time `gorm:"column:last_received_at;type:timestamp"                   json:"last_received_at,omitempty"`
	ScannedCount              uint       `gorm:"column:scanned_count;type:int unsigned;not null;default:0" json:"scanned_count"`
	HitCount                  uint       `gorm:"column:hit_count;type:int unsigned;not null;default:0"     json:"hit_count"`
	FailureCount              uint       `gorm:"column:failure_count;type:int unsigned;not null;default:0" json:"failure_count"`
	ScannedAt                 time.Time  `gorm:"column:scanned_at;type:timestamp;not null;default:CURRENT_TIMESTAMP" json:"scanned_at"`
	QuarantineMailbox         *string    `gorm:"column:quarantine_mailbox;type:varchar(64)"               json:"quarantine_mailbox,omitempty"`
	QuarantineMailboxVerified *time.Time `gorm:"column:quarantine_mailbox_verified;type:timestamp"        json:"quarantine_mailbox_verified,omitempty"`
}

func (MailScanState) TableName() string { return "mail_scan_state" }

// MailScanFailure is a DLQ row. The scanner emits one per yr-exec
// failure, JMAP 5xx, or blob-fetch error so operators can see what's
// blocking conversion without reading logs.
type MailScanFailure struct {
	ID          string    `gorm:"column:id;type:varchar(26);primaryKey" json:"id"`
	AccountID   string    `gorm:"column:account_id;type:varchar(64);not null;index:idx_mail_scan_failures_account,priority:1" json:"account_id"`
	MailboxID   string    `gorm:"column:mailbox_id;type:varchar(64);not null;index:idx_mail_scan_failures_account,priority:2" json:"mailbox_id"`
	EmailID     *string   `gorm:"column:email_id;type:varchar(64)"      json:"email_id,omitempty"`
	Attachment  *string   `gorm:"column:attachment;type:varchar(255)"   json:"attachment,omitempty"`
	Reason      string    `gorm:"column:reason;type:varchar(64);not null" json:"reason"`
	Detail      *string   `gorm:"column:detail;type:text"               json:"detail,omitempty"`
	AttemptedAt time.Time `gorm:"column:attempted_at;type:timestamp;not null;default:CURRENT_TIMESTAMP;index:idx_mail_scan_failures_at" json:"attempted_at"`
}

func (MailScanFailure) TableName() string { return "mail_scan_failures" }
