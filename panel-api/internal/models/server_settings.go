package models

import "time"

// ServerSettings is the single-row table holding server identity and
// DNS configuration. Operators edit these from the admin Settings page;
// at first boot the row is seeded from the installer's config.toml.
type ServerSettings struct {
	ID          uint8     `gorm:"primaryKey;default:1"                json:"id"`
	Hostname    string    `gorm:"type:varchar(253);not null;default:''" json:"hostname"`
	PublicIPv4  string    `gorm:"type:varchar(45);not null;default:''"  json:"public_ipv4"`
	PublicIPv6  string    `gorm:"type:varchar(45);not null;default:''"  json:"public_ipv6"`
	NS1Name     string    `gorm:"type:varchar(253);not null;default:''" json:"ns1_name"`
	NS1IPv4     string    `gorm:"type:varchar(45);not null;default:''"  json:"ns1_ipv4"`
	NS2Name     string    `gorm:"type:varchar(253);not null;default:''" json:"ns2_name"`
	NS2IPv4     string    `gorm:"type:varchar(45);not null;default:''"  json:"ns2_ipv4"`
	AdminEmail  string    `gorm:"type:varchar(320);not null;default:''" json:"admin_email"`
	// DefaultPHPVersion is the PHP version new user pools are seeded with
	// (reconciler default-pool path) and the version the admin UI pre-selects.
	// Admin changes it via POST /admin/php/versions/:version/default; agent
	// php.version.status reports it in default_version. Schema default '8.5'.
	DefaultPHPVersion string `gorm:"type:varchar(8);not null;default:'8.5'" json:"default_php_version"`
	Timezone           string `gorm:"type:varchar(64);not null;default:''"   json:"timezone"`
	SSHPort            uint16 `gorm:"type:int unsigned;not null;default:22"  json:"ssh_port"`
	// SSHPasswordAuth governs the GLOBAL PasswordAuthentication directive
	// in /etc/ssh/sshd_config.d/jabali-sshd.conf. In practice this only
	// affects users NOT in the jabali-sftp group (root, admin shell users)
	// because the M12 Match Group jabali-sftp block in jabali-sftp.conf
	// overrides the global for hosting users. Use SSHUserPasswordAuth to
	// flip the per-group setting.
	SSHPasswordAuth     bool   `gorm:"type:tinyint(1);not null;default:0"     json:"ssh_password_auth"`
	// SSHUserPasswordAuth governs PasswordAuthentication inside the
	// Match Group jabali-sftp block — i.e. for hosting users. The agent's
	// system.set_ssh_config rewrites jabali-sftp.conf to honor this flag.
	// Note: ForceCommand internal-sftp still applies, so password-authed
	// hosting users land in SFTP, not a shell — per-package shell access
	// is a separate, future feature.
	SSHUserPasswordAuth bool   `gorm:"type:tinyint(1);not null;default:0"     json:"ssh_user_password_auth"`
	UpdatedAt          time.Time `gorm:"type:datetime(6);not null"             json:"updated_at"`
}

func (ServerSettings) TableName() string { return "server_settings" }
