package models

import "time"

// BWDaily — per-domain daily bandwidth and request totals harvested
// from goaccess output of nginx access logs (M13.1).
//
// Composite primary key (DomainID, Day) enforces one row per domain
// per UTC date; the agent's scan handler upserts.
type BWDaily struct {
	DomainID      string    `gorm:"column:domain_id;type:varchar(26);primaryKey" json:"domain_id"`
	Day           time.Time `gorm:"column:day;type:date;primaryKey" json:"day"`
	BytesTotal    uint64    `gorm:"column:bytes_total;type:bigint unsigned;not null;default:0" json:"bytes_total"`
	RequestsTotal uint64    `gorm:"column:requests_total;type:bigint unsigned;not null;default:0" json:"requests_total"`
	UpdatedAt     time.Time `gorm:"column:updated_at;type:datetime(6);not null" json:"updated_at"`
}

func (BWDaily) TableName() string { return "bw_daily" }
