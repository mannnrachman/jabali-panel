package models

import "time"

// The actual token bytes are never stored; only SHA-256(token) is persisted.
type FileBrowserSSOToken struct {
	// ID is a 26-char ULID (Crockford base32).
	ID string `gorm:"type:char(26);primaryKey" json:"id"`

	// UserID references the panel user initiating the SSO flow.
	UserID string `gorm:"type:char(26);not null;index:idx_user_id" json:"user_id"`

	// TokenHash is the lowercase hex SHA-256 of the raw token.
	// This is what the validator endpoint checks.
	TokenHash string `gorm:"type:char(64);not null;uniqueIndex:uniq_token_hash" json:"token_hash"`

	// ExpiresAt is when this token becomes invalid (60 seconds from creation).
	ExpiresAt time.Time `gorm:"type:datetime(6);not null;index:idx_expires_at" json:"expires_at"`

	// CreatedAt is the token creation timestamp.
	CreatedAt time.Time `gorm:"type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"created_at"`

	// UsedAt is set to the current time when the token is first successfully validated.
	// NULL until then. One-time use: cannot be reused.
	UsedAt *time.Time `gorm:"type:datetime(6)" json:"used_at,omitempty"`
}

// TableName pins the plural form in case GORM ever changes its default.
func (FileBrowserSSOToken) TableName() string { return "filebrowser_sso_tokens" }
