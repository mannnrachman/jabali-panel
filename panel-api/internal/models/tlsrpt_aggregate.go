package models

import "time"

// TLSRPTAggregate is one ROW (per policy result-type bucket) from
// one TLS-RPT (RFC 8460) aggregate report. Schema lives in mig 000139.
// Append-only, 90-day retention. Pruned app-side; never UPDATEd.
type TLSRPTAggregate struct {
	ID           string    `gorm:"column:id;type:char(26);primaryKey" json:"id"`
	Domain       string    `gorm:"column:domain;type:varchar(253);not null;index:idx_tlsrpt_domain_window,priority:1" json:"domain"`
	Reporter     string    `gorm:"column:reporter;type:varchar(253);not null" json:"reporter"`
	WindowStart  time.Time `gorm:"column:window_start;type:datetime;not null" json:"window_start"`
	WindowEnd    time.Time `gorm:"column:window_end;type:datetime;not null;index:idx_tlsrpt_domain_window,priority:2" json:"window_end"`
	ResultType   string    `gorm:"column:result_type;type:varchar(48);not null;default:''" json:"result_type"`
	SuccessCount uint      `gorm:"column:success_count;type:int unsigned;not null;default:0" json:"success_count"`
	FailureCount uint      `gorm:"column:failure_count;type:int unsigned;not null;default:0" json:"failure_count"`
	CreatedAt    time.Time `gorm:"column:created_at;type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"created_at"`
}

func (TLSRPTAggregate) TableName() string { return "tlsrpt_aggregate" }
