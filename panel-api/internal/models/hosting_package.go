package models

import (
	"time"

	"gorm.io/gorm"
)

// HostingPackage defines a quota bundle that admins assign to hosting users.
// Quotas are soft limits enforced by the agent at provisioning time and
// checked by periodic sync jobs. The panel stores them; the agent enforces.
type HostingPackage struct {
	ID   string `gorm:"type:char(26);primaryKey" json:"id"`
	Name string `gorm:"type:varchar(100);uniqueIndex:ux_packages_name;not null" json:"name"`

	// Quotas — zero means unlimited for that resource.
	DiskQuotaMB      uint32 `gorm:"type:int unsigned;not null;default:0" json:"disk_quota_mb"`
	BandwidthQuotaMB uint32 `gorm:"type:int unsigned;not null;default:0" json:"bandwidth_quota_mb"`
	MaxDomains       uint32 `gorm:"type:int unsigned;not null;default:0" json:"max_domains"`
	MaxEmailAccounts uint32 `gorm:"type:int unsigned;not null;default:0" json:"max_email_accounts"`
	MaxDatabases     uint32 `gorm:"type:int unsigned;not null;default:0" json:"max_databases"`
	MaxFTPAccounts   uint32 `gorm:"type:int unsigned;not null;default:0" json:"max_ftp_accounts"`

	// Feature toggles.
	SSHEnabled bool `gorm:"type:tinyint(1);not null;default:0" json:"ssh_enabled"`
	CGIEnabled bool `gorm:"type:tinyint(1);not null;default:0" json:"cgi_enabled"`

	CreatedAt time.Time      `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt time.Time      `gorm:"type:datetime(6);not null" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"type:datetime(6);index:ix_packages_deleted_at" json:"-"`
}

func (HostingPackage) TableName() string { return "hosting_packages" }
