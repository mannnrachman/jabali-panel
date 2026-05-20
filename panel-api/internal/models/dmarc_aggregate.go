package models

import "time"

// DMARCAggregate is one ROW (per-source-IP per-disposition bucket)
// from one DMARC RUA aggregate report (RFC 7489 Appendix C). Reports
// arrive in the operator's rua mailbox as gzipped XML; the ingest
// path explodes each `<record>` × `<auth_results>` into one row.
// Append-only, never UPDATEd — the table grows reporter × domain ×
// window × source and is pruned app-side on a 90-day retention
// (mig 000139 note).
type DMARCAggregate struct {
	ID          string    `gorm:"column:id;type:char(26);primaryKey" json:"id"`
	Domain      string    `gorm:"column:domain;type:varchar(253);not null;index:idx_dmarc_domain_window,priority:1" json:"domain"`
	Reporter    string    `gorm:"column:reporter;type:varchar(253);not null" json:"reporter"`
	WindowStart time.Time `gorm:"column:window_start;type:datetime;not null" json:"window_start"`
	WindowEnd   time.Time `gorm:"column:window_end;type:datetime;not null;index:idx_dmarc_domain_window,priority:2" json:"window_end"`
	SourceIP    string    `gorm:"column:source_ip;type:varchar(45);not null" json:"source_ip"`
	Disposition string    `gorm:"column:disposition;type:varchar(16);not null" json:"disposition"` // none|quarantine|reject
	DKIM        string    `gorm:"column:dkim;type:varchar(8);not null" json:"dkim"`               // pass|fail
	SPF         string    `gorm:"column:spf;type:varchar(8);not null" json:"spf"`
	Cnt         uint      `gorm:"column:cnt;type:int unsigned;not null;default:0" json:"cnt"`
	CreatedAt   time.Time `gorm:"column:created_at;type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"created_at"`
}

func (DMARCAggregate) TableName() string { return "dmarc_aggregate" }
