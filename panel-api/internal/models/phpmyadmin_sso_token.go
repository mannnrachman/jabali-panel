package models

import "time"

// PhpMyAdminSSOToken represents a single-use token for phpMyAdmin SSO.
// Tokens are short-lived (5 minutes) and deleted on consume.
// The actual token bytes are never stored; only SHA-256(token) is persisted.
type PhpMyAdminSSOToken struct {
	// ID is a 26-char ULID (Crockford base32).
	ID string `gorm:"type:char(26);primaryKey" json:"id"`

	// UserID references the panel user initiating the SSO flow.
	UserID string `gorm:"type:char(26);not null;index:idx_user_id" json:"user_id"`

	// DatabaseID references the database the user is accessing via phpMyAdmin.
	DatabaseID string `gorm:"type:char(26);not null" json:"database_id"`

	// TokenHash is the lowercase hex SHA-256 of the raw token.
	// This is what httpMyAdmin's /sso/phpmyadmin/validate endpoint checks.
	TokenHash string `gorm:"type:char(64);not null;uniqueIndex:uniq_token_hash" json:"token_hash"`

	// ExpiresAt is when this token becomes invalid (5 minutes from creation).
	ExpiresAt time.Time `gorm:"type:datetime(6);not null;index:idx_expires_at" json:"expires_at"`

	// CreatedAt is the token creation timestamp.
	CreatedAt time.Time `gorm:"type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"created_at"`
}

// TableName pins the table name for GORM.
func (PhpMyAdminSSOToken) TableName() string { return "phpmyadmin_sso_tokens" }
