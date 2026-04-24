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

	// VAPIDPublicKey / VAPIDPrivateKey / VAPIDSubject hold the Web Push
	// keypair + subject mint. See ADR-0057. Generated on first boot by
	// ServerSettingsRepository.EnsureVAPID (called from serve.go seed
	// goroutine), NOT by the migration — per
	// feedback_migration_data_seed_ordering. All three columns are
	// nullable to represent "not yet seeded" on fresh boot; once
	// populated they remain stable until an explicit operator rotation
	// (deferred to a future milestone).
	//
	// json:"-" keeps the private_key out of the server_settings GET
	// response served to the admin UI. The public_key and subject are
	// exposed through a dedicated /api/v1/admin/vapid/public endpoint
	// in Step 5 (so the SPA can register the service worker) rather
	// than leaking through the generic settings endpoint.
	VAPIDPublicKey  *string `gorm:"type:varchar(128);column:vapid_public_key"  json:"-"`
	VAPIDPrivateKey *string `gorm:"type:varchar(64);column:vapid_private_key"  json:"-"`
	VAPIDSubject    *string `gorm:"type:varchar(320);column:vapid_subject"     json:"-"`

	// M26 ModSecurity globals (migration 000066, ADR-0055). The agent
	// command security.modsec.global.set rewrites both these columns
	// AND the on-disk /etc/nginx/modsecurity.conf SecRuleEngine
	// directive in one transaction. Paranoia level is the OWASP CRS
	// 1..4 scale; admin UI clamps to that range.
	// Column names pinned explicitly — GORM's default snake_case on
	// ModSec* would emit mod_sec_* which doesn't match migration 000066.
	ModSecGlobalEnabled bool  `gorm:"column:modsec_global_enabled;type:tinyint(1);not null;default:0"        json:"modsec_global_enabled"`
	ModSecParanoiaLevel uint8 `gorm:"column:modsec_paranoia_level;type:tinyint unsigned;not null;default:1"  json:"modsec_paranoia_level"`

	// M28 Panel Branding. PanelBrandText is the short label next to
	// the logo and in the browser title. LogoLightPath / LogoDarkPath
	// hold absolute on-disk paths to operator-uploaded logo files
	// under /var/lib/jabali-panel/branding/; empty string means "no
	// logo uploaded — fall back to the built-in default".
	PanelBrandText string `gorm:"column:panel_brand_text;type:varchar(60);not null;default:''" json:"panel_brand_text"`
	LogoLightPath  string `gorm:"column:logo_light_path;type:varchar(255);not null;default:''" json:"logo_light_path"`
	LogoDarkPath   string `gorm:"column:logo_dark_path;type:varchar(255);not null;default:''"  json:"logo_dark_path"`

	// M26 AppSec geoblock (migration 000067). Server-wide rule applied
	// by crowdsec AppSec. Mode ∈ {"off", "allow", "deny"}:
	//   off   — rule file written with no filter; all traffic passes
	//   allow — requests from listed countries pass; everything else 403
	//   deny  — requests from listed countries 403; everything else passes
	// Countries is a comma-separated list of ISO 3166-1 alpha-2 codes.
	// "off" + empty is the default; toggling needs nginx AppSec wiring
	// (see plans/m26-security-tab-runbook.md).
	AppSecGeoblockMode      string `gorm:"column:appsec_geoblock_mode;type:varchar(10);not null;default:'off'"    json:"appsec_geoblock_mode"`
	AppSecGeoblockCountries string `gorm:"column:appsec_geoblock_countries;type:varchar(1000);not null;default:''" json:"appsec_geoblock_countries"`

	UpdatedAt time.Time `gorm:"type:datetime(6);not null"             json:"updated_at"`
}

func (ServerSettings) TableName() string { return "server_settings" }
