package models

import "time"

// RefreshToken is a rotation-tracked refresh-token row. The actual token
// material is never stored: we keep only the SHA-256 hash, so DB compromise
// doesn't yield usable tokens.
//
// Rotation semantics (implemented in Phase 4):
//   - On /auth/refresh: the incoming token is looked up by token_hash, the
//     row is locked (SELECT ... FOR UPDATE), RevokedAt is set, and a NEW row
//     with a new token_hash is inserted in the same transaction.
//   - On /auth/logout: RevokedAt is set.
//   - The UNIQUE index on token_hash guarantees at most one live token per
//     hash, so concurrent refresh attempts race cleanly.
type RefreshToken struct {
	ID     string `gorm:"type:char(26);primaryKey"     json:"id"`
	UserID string `gorm:"type:char(26);not null;index:ix_refresh_tokens_user" json:"user_id"`

	// ForeignKey to users.id with ON DELETE CASCADE — when a user row is
	// hard-deleted, their refresh tokens go with it. (Soft-deleted users
	// keep their refresh rows so deletion can be reversed.)
	User User `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT" json:"-"`

	// DeviceID is an opaque stable ID for the client device (User-Agent hash
	// or a UUID minted at first-login). Enables per-device revocation.
	DeviceID string `gorm:"type:varchar(255);not null" json:"device_id"`

	// TokenHash is hex(SHA-256(token)). UNIQUE so duplicate insertion fails
	// fast and concurrent-rotation races are resolved by the DB.
	TokenHash string `gorm:"type:char(64);uniqueIndex:ux_refresh_tokens_hash;not null" json:"-"`

	ExpiresAt  time.Time  `gorm:"type:datetime(6);not null;index:ix_refresh_tokens_expires" json:"expires_at"`
	RevokedAt  *time.Time `gorm:"type:datetime(6)"                                          json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `gorm:"type:datetime(6)"                                          json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `gorm:"type:datetime(6);not null"                                 json:"created_at"`
}

// TableName keeps the explicit snake_case plural, matching golang-migrate.
func (RefreshToken) TableName() string { return "refresh_tokens" }
