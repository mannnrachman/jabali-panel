package models

import "time"

// DomainDNSSECKey caches a single DNSSEC key as reported by
// `pdnsutil show-zone`. Advisory — the authoritative source is PowerDNS's
// own `cryptokeys` table. Private key material is NEVER stored here
// (ADR-0076).
type DomainDNSSECKey struct {
	DomainID   string    `gorm:"primaryKey;type:char(26);not null" json:"domain_id"`
	KeyTag     int       `gorm:"primaryKey;type:int;not null" json:"key_tag"`
	KeyType    string    `gorm:"type:enum('KSK','ZSK','CSK');not null" json:"key_type"`
	Algorithm  uint8     `gorm:"type:tinyint unsigned;not null" json:"algorithm"`
	PublicKey  string    `gorm:"type:text;not null" json:"public_key"`
	Active     bool      `gorm:"type:tinyint(1);not null;default:1" json:"active"`
	ObservedAt time.Time `gorm:"type:datetime(6);not null" json:"observed_at"`
}

// TableName returns the MariaDB table name for DomainDNSSECKey.
func (DomainDNSSECKey) TableName() string { return "domain_dnssec_keys" }
