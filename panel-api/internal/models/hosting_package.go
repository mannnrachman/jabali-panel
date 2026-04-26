package models

import (
	"time"
)

// HostingPackage defines a quota bundle that admins assign to hosting users.
// Quotas are soft limits enforced by the agent at provisioning time and
// checked by periodic sync jobs. The panel stores them; the agent enforces.
type HostingPackage struct {
	ID   string `gorm:"type:char(26);primaryKey" json:"id"`
	Name string `gorm:"type:varchar(100);uniqueIndex:ux_packages_name;not null" json:"name"`

	// Quotas — zero means unlimited for that resource.
	DiskQuotaMB       uint32 `gorm:"type:int unsigned;not null;default:0" json:"disk_quota_mb"`
	// Resource limits (M18). Enforced via POSIX user quota (disk) +
	// cgroups v2 drop-in on the per-user slice (cpu/memory/io/tasks).
	// Zero = unlimited for every field — the agent omits the systemd
	// directive entirely rather than emitting "CPUQuota=0%".
	CPUQuotaPercent  uint32 `gorm:"type:int unsigned;not null;default:0" json:"cpu_quota_percent"`
	MemoryLimitMB    uint32 `gorm:"type:int unsigned;not null;default:0" json:"memory_limit_mb"`
	IOReadMbps       uint32 `gorm:"type:int unsigned;not null;default:0" json:"io_read_mbps"`
	IOWriteMbps      uint32 `gorm:"type:int unsigned;not null;default:0" json:"io_write_mbps"`
	MaxTasks         uint32 `gorm:"type:int unsigned;not null;default:0" json:"max_tasks"`
	BandwidthQuotaMB  uint32 `gorm:"type:int unsigned;not null;default:0" json:"bandwidth_quota_mb"`
	MaxDomains        uint32 `gorm:"type:int unsigned;not null;default:0" json:"max_domains"`
	MaxEmailAccounts  uint32 `gorm:"type:int unsigned;not null;default:0" json:"max_email_accounts"`
	MaxDatabases      uint32 `gorm:"type:int unsigned;not null;default:0" json:"max_databases"`
	MaxDatabaseUsers  uint32 `gorm:"type:int unsigned;not null;default:0" json:"max_database_users"`
	MaxFTPAccounts    uint32 `gorm:"type:int unsigned;not null;default:0" json:"max_ftp_accounts"`

	// Feature toggles.
	SSHEnabled bool `gorm:"type:tinyint(1);not null;default:0" json:"ssh_enabled"`
	CGIEnabled bool `gorm:"type:tinyint(1);not null;default:0" json:"cgi_enabled"`

	// NspawnImageVersion (M13 / ADR-0067) pins users on this package to a
	// specific systemd-nspawn rootfs at /var/lib/jabali-nspawn/images/<v>/.
	// NULL → reconciler stamps from server_settings.default_nspawn_image_version
	// at next sweep. Only takes effect when ssh_sandbox_mode=nspawn AND the
	// package has ssh_enabled=true.
	NspawnImageVersion *string `gorm:"type:varchar(64);column:nspawn_image_version" json:"nspawn_image_version,omitempty"`

	CreatedAt time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (HostingPackage) TableName() string { return "hosting_packages" }
