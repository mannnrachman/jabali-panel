package models

import "time"

// AdminerSSOToken represents a single-use token for Adminer SSO.
// Mirrors PhpMyAdminSSOToken but adds an engine discriminator so a
// single bridge serves both MariaDB and PostgreSQL via the Adminer
// jabali-sso plugin (which calls /sso/adminer/validate over UDS and
// receives engine-specific credentials in return).
//
// Tokens are short-lived (5 minutes) and deleted on consume.
// Only SHA-256(token) is persisted; raw bytes never hit disk.
type AdminerSSOToken struct {
	// ID is a 26-char ULID (Crockford base32).
	ID string `gorm:"type:char(26);primaryKey" json:"id"`

	// UserID references the panel user initiating the SSO flow.
	UserID string `gorm:"type:char(26);not null;index:idx_adminer_user_id" json:"user_id"`

	// DatabaseID references the database the user is accessing via Adminer.
	DatabaseID string `gorm:"type:char(26);not null" json:"database_id"`

	// Engine is the database engine — drives which shadow account
	// (mysqladmin_* vs pgadmin_*) the validate handler returns.
	Engine string `gorm:"type:enum('mariadb','postgres');not null;index:idx_adminer_engine" json:"engine"`

	// TokenHash is the lowercase hex SHA-256 of the raw token.
	TokenHash string `gorm:"type:char(64);not null;uniqueIndex:uniq_adminer_token_hash" json:"token_hash"`

	// ExpiresAt is when this token becomes invalid.
	ExpiresAt time.Time `gorm:"type:datetime(6);not null;index:idx_adminer_expires_at" json:"expires_at"`

	// CreatedAt is the token creation timestamp.
	CreatedAt time.Time `gorm:"type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"created_at"`
}

func (AdminerSSOToken) TableName() string { return "adminer_sso_tokens" }
