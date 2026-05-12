package models

import "time"

// MigrationAccountSizeCache memoizes per-account `du -sh` results
// returned by Discoverer.AccountSize so re-discovery within 24h is
// instant. Keyed by (host, source_user) — host matches the wizard's
// connection target. ADR-0095 decision 6.
type MigrationAccountSizeCache struct {
	ID         uint64    `gorm:"column:id;primaryKey;autoIncrement"               json:"id"`
	Host       string    `gorm:"column:host;type:varchar(255);not null;uniqueIndex:uniq_host_user,priority:1" json:"host"`
	SourceUser string    `gorm:"column:source_user;type:varchar(255);not null;uniqueIndex:uniq_host_user,priority:2" json:"source_user"`
	SizeBytes  int64     `gorm:"column:size_bytes;type:bigint unsigned;not null"  json:"size_bytes"`
	FetchedAt  time.Time `gorm:"column:fetched_at;type:datetime;not null"         json:"fetched_at"`
}

func (MigrationAccountSizeCache) TableName() string { return "migration_account_size_cache" }
