// Package models holds the GORM struct definitions that mirror the panel's
// own database schema (the `jabali_panel` DB on MariaDB). Customer-hosted
// databases are a separate concern and are NOT modelled here.
package models

import (
	"time"
)

// User is a panel account. Two personas share the table:
//
//   - Admin users: panel operators (is_admin=true). Managed by the
//     seed/bootstrap flow; is_admin is never settable via the public API.
//   - Hosting users: panel customers (is_admin=false) mapped to a Linux UID
//     once the agent provisions the OS user; linux_uid is NULL until then.
type User struct {
	// ID is a 26-char ULID (Crockford base32). See internal/ids.
	ID string `gorm:"type:char(26);primaryKey" json:"id"`

	Email string `gorm:"type:varchar(255);uniqueIndex:ux_users_email;not null" json:"email"`

	// Username is the Linux account name for this user. NULL for admins
	// (panel-only, no OS account). Must match POSIX regex
	// ^[a-z_][a-z0-9_-]{0,31}$. Unique across the panel.
	Username *string `gorm:"type:varchar(32);uniqueIndex:ux_users_username" json:"username,omitempty"`

	NameFirst    string `gorm:"type:varchar(100);not null;default:''"                 json:"name_first"`
	NameLast     string `gorm:"type:varchar(100);not null;default:''"                 json:"name_last"`
	PasswordHash string `gorm:"type:varchar(255);not null"                            json:"-"`

	// IsAdmin is stored as TINYINT(1). Never written from a request DTO —
	// only by an admin-only handler (Phase 6) or the bootstrap seed.
	IsAdmin bool `gorm:"type:tinyint(1);not null;default:0" json:"is_admin"`

	// PackageID links to hosting_packages. NULL for admin-only accounts.
	PackageID *string `gorm:"type:char(26)" json:"package_id,omitempty"`

	// LinuxUID is set by the handler after the agent successfully creates
	// the OS user. NULL until then, or for admin-only accounts.
	LinuxUID *uint32 `gorm:"type:int unsigned" json:"linux_uid,omitempty"`

	// MySQL Admin Shadow Account fields for phpMyAdmin SSO.
	// MysqladminUsername is the shadow MariaDB user created for SSO.
	// NULL until first SSO provision.
	MysqladminUsername *string `gorm:"type:varchar(64)" json:"mysqladmin_username,omitempty"`

	// MysqladminPasswordEnc is the AES-256-GCM encrypted password for the
	// shadow account. Never exported in API responses. NULL until first SSO provision.
	MysqladminPasswordEnc []byte `gorm:"type:varbinary(512)" json:"-"`

	// MysqladminProvisionedAt is the timestamp of the first SSO provision.
	// NULL until the shadow account is created.
	MysqladminProvisionedAt *time.Time `gorm:"type:datetime(6)" json:"mysqladmin_provisioned_at,omitempty"`

	// TOTPSecretEncrypted holds the AES-256-GCM-sealed TOTP shared secret
	// (via internal/ssokey). NULL when the user has never started enrolment.
	// Present but TOTPEnabled=false during the enrol-but-not-verified window.
	TOTPSecretEncrypted []byte `gorm:"column:totp_secret_encrypted;type:varbinary(256)" json:"-"`

	// TOTPEnabled becomes true only after the user verifies the first code,
	// which also generates the 10 backup codes. Login flow consults this
	// to decide whether to short-circuit with a 2fa_pending token.
	TOTPEnabled bool `gorm:"column:totp_enabled;type:tinyint(1);not null;default:0" json:"totp_enabled"`

	// TOTPEnabledAt records when 2FA was successfully turned on. Useful for
	// audit/observability; not load-bearing for auth.
	TOTPEnabledAt *time.Time `gorm:"column:totp_enabled_at;type:datetime(6)" json:"totp_enabled_at,omitempty"`

	CreatedAt time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

// TableName pins the plural form in case GORM ever changes its default.
func (User) TableName() string { return "users" }
