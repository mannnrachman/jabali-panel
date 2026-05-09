package models

import (
	"encoding/json"
	"time"
)

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

	// M27 Step 5 — captcha remediation for crowdsec-nginx-bouncer.
	// Secret is plaintext-at-rest (convention matches kratos_admin_secret,
	// vapid_private_key, smtp_relay_password) but WRITE-ONLY at the API
	// edge — GET responses NEVER include the secret.
	CrowdSecCaptchaEnabled   bool   `gorm:"column:crowdsec_captcha_enabled;type:boolean;not null;default:false"       json:"crowdsec_captcha_enabled"`
	CrowdSecCaptchaProvider  string `gorm:"column:crowdsec_captcha_provider;type:varchar(32);not null;default:''"     json:"crowdsec_captcha_provider"`
	CrowdSecCaptchaSiteKey   string `gorm:"column:crowdsec_captcha_site_key;type:varchar(512);not null;default:''"    json:"crowdsec_captcha_site_key"`
	CrowdSecCaptchaSecretKey string `gorm:"column:crowdsec_captcha_secret_key;type:varchar(512);not null;default:''"  json:"-"`

	// Global disk-quota toggle (migration 000071). When false (default),
	// the reconciler does not apply POSIX user quota and the Packages UI
	// disables the disk-quota fields. cgroups limits (cpu/memory/io/
	// tasks) are independent of this flag and always apply.
	//
	// Operator must independently mount /home on its own filesystem with
	// usrquota,grpquota options before flipping this true. install.sh
	// refuses to enable quota when /home == / (M18); this DB flag is the
	// runtime equivalent — flipping it true on an unprepared host means
	// agent quota.apply will fail and the panel will surface the error.
	DiskQuotaEnabled bool `gorm:"column:disk_quota_enabled;type:tinyint(1);not null;default:0" json:"disk_quota_enabled"`

	// Bandwidth-quota auto-suspend toggle (M13.1.1). When true, the
	// reconciler disables every owned domain of a user whose monthly
	// bytes ≥ BandwidthQuotaMB and re-enables when ≤ 80%. Off by default.
	BandwidthQuotaEnforceEnabled bool `gorm:"column:bandwidth_quota_enforce_enabled;type:tinyint(1);not null;default:0" json:"bandwidth_quota_enforce_enabled"`

	// File-manager upload cap (MB). Enforced by panel-api at request
	// time via http.MaxBytesReader; nginx vhost client_max_body_size
	// is set to the static ceiling (1G) so this is the operator-tunable
	// knob. Migration 000073. 0 falls back to the compile-time default.
	UploadMaxSizeMB uint32 `gorm:"column:upload_max_size_mb;type:int unsigned;not null;default:1024" json:"upload_max_size_mb"`

	// M13 SSH shell sandbox (ADR-0067).
	// SSHSandboxMode ∈ {"bubblewrap", "nspawn"}. Default bubblewrap on
	// fresh installs. Wrapper at /usr/local/bin/jabali-ssh-shell reads
	// /etc/jabali/ssh-sandbox-mode (kept in lockstep with this column
	// by system.set_ssh_sandbox_mode) on every connect, so toggling the
	// mode does NOT require a sshd reload.
	SSHSandboxMode string `gorm:"column:ssh_sandbox_mode;type:varchar(16);not null;default:'bubblewrap'" json:"ssh_sandbox_mode"`
	// DefaultNspawnImageVersion is the image new SSH-enabled users are
	// stamped with at user-create time (reconciler stamps NULL pins).
	// Existing users keep their pin even after the default bumps —
	// upgrades are an explicit admin action. Format: [a-z0-9-]+ matching
	// the directory under /var/lib/jabali-nspawn/images/.
	DefaultNspawnImageVersion string `gorm:"column:default_nspawn_image_version;type:varchar(64);not null;default:'debian-13-v1'" json:"default_nspawn_image_version"`

	// M30 backup-restore retention knobs (migration 000085). Pruning runs
	// daily via the jabali-backup-retention.timer drop-in; values mirror
	// `restic forget --keep-daily/--keep-weekly/--keep-monthly`. Empty
	// BackupRemoteURL keeps backups at the local repo
	// /var/lib/jabali-backups/repo. M30.1 wires remote backends; v1 leaves
	// these unused.
	BackupKeepDaily            uint32 `gorm:"column:backup_keep_daily;type:int unsigned;not null;default:7"     json:"backup_keep_daily"`
	BackupKeepWeekly           uint32 `gorm:"column:backup_keep_weekly;type:int unsigned;not null;default:4"    json:"backup_keep_weekly"`
	BackupKeepMonthly          uint32 `gorm:"column:backup_keep_monthly;type:int unsigned;not null;default:6"   json:"backup_keep_monthly"`
	BackupRemoteURL            string `gorm:"column:backup_remote_url;type:varchar(512);not null;default:''"    json:"backup_remote_url"`
	BackupRemoteCredentialsRef string `gorm:"column:backup_remote_credentials_ref;type:varchar(128);not null;default:''" json:"backup_remote_credentials_ref"`

	// BackupMaxConcurrentJobs caps how many backup_jobs the in-process
	// dispatcher will keep in status=running at once. Scheduler ticks
	// enqueue rows as queued; the dispatcher drains them under this
	// ceiling. Migration 000096. 0 falls back to default 2.
	BackupMaxConcurrentJobs uint32 `gorm:"column:backup_max_concurrent_jobs;type:int unsigned;not null;default:2" json:"backup_max_concurrent_jobs"`

	// M34 per-user PHP-FPM egress firewall defaults (migration 000102).
	// NULL on the JSON columns means "use the agent's CanonicalDefaults()";
	// non-null arrays (including empty []) override. Burst threshold is
	// drops/tick that fires the M14 egress_drop_burst event source.
	EgressDefaultLoopbackCIDRs  *json.RawMessage `gorm:"column:egress_default_loopback_cidrs;type:json"          json:"egress_default_loopback_cidrs,omitempty"`
	EgressDefaultLoopback6CIDRs *json.RawMessage `gorm:"column:egress_default_loopback6_cidrs;type:json"         json:"egress_default_loopback6_cidrs,omitempty"`
	EgressDefaultPortsTCP       *json.RawMessage `gorm:"column:egress_default_ports_tcp;type:json"               json:"egress_default_ports_tcp,omitempty"`
	EgressDefaultPortsUDP       *json.RawMessage `gorm:"column:egress_default_ports_udp;type:json"               json:"egress_default_ports_udp,omitempty"`
	EgressBurstThreshold        uint32  `gorm:"column:egress_burst_threshold;type:int unsigned;not null;default:50" json:"egress_burst_threshold"`

	// M37 PostgreSQL parity (Phase 1, ADR-0091). Migration 000111.
	// PostgresEnabled gates the engine-discriminator code path —
	// fresh installs ship PG service installed but disabled; admin
	// flips this true in Server Settings to start using PG.
	// PostgresMaxConnectionsPerUser is surfaced when Wave A creates
	// per-user PG roles (mirrors MariaDB max_user_connections cap).
	PostgresEnabled               bool   `gorm:"column:postgres_enabled;type:tinyint(1);not null;default:0" json:"postgres_enabled"`
	PostgresMaxConnectionsPerUser uint16 `gorm:"column:postgres_max_connections_per_user;type:smallint unsigned;not null;default:25" json:"postgres_max_connections_per_user"`

	UpdatedAt time.Time `gorm:"type:datetime(6);not null"             json:"updated_at"`
}

func (ServerSettings) TableName() string { return "server_settings" }
