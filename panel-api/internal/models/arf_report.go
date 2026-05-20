package models

import "time"

// ARFReport is one ROW (per ARF feedback envelope) from the inbound
// abuse-reports stream. Stalwart's ArfExternalReport schema is the
// upstream; mail_abuse_ingest writes here.
//
// stalwart_id holds the upstream object id so re-runs of the ingest
// loop (or operator-triggered backfills) skip already-imported
// reports via the UNIQUE index.
type ARFReport struct {
	ID                string     `gorm:"column:id;type:char(26);primaryKey" json:"id"`
	StalwartID        string     `gorm:"column:stalwart_id;type:varchar(128);not null;uniqueIndex:uq_arf_stalwart_id" json:"stalwart_id"`
	ReceivedAt        time.Time  `gorm:"column:received_at;type:datetime;not null;index:idx_arf_received" json:"received_at"`
	FeedbackType      string     `gorm:"column:feedback_type;type:varchar(32);not null;default:'abuse'" json:"feedback_type"`
	Reporter          string     `gorm:"column:reporter;type:varchar(253);not null;default:''" json:"reporter"`
	OriginalRcpt      string     `gorm:"column:original_rcpt;type:varchar(320);not null;default:''" json:"original_rcpt"`
	OriginalMailFrom  string     `gorm:"column:original_mail_from;type:varchar(320);not null;default:'';index:idx_arf_rcpt" json:"original_mail_from"`
	SourceIP          string     `gorm:"column:source_ip;type:varchar(45);not null;default:''" json:"source_ip"`
	Incidents         uint       `gorm:"column:incidents;type:int unsigned;not null;default:1" json:"incidents"`
	UserAgent         string     `gorm:"column:user_agent;type:varchar(255);not null;default:''" json:"user_agent"`
	ReportingMTA      string     `gorm:"column:reporting_mta;type:varchar(253);not null;default:''" json:"reporting_mta"`
	ArrivalDate       *time.Time `gorm:"column:arrival_date;type:datetime" json:"arrival_date,omitempty"`
	CreatedAt         time.Time  `gorm:"column:created_at;type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"created_at"`
}

func (ARFReport) TableName() string { return "arf_report" }
