package models

import "time"

// MagicLinkToken represents a single-use magic link token for user authentication.
// Tokens are short-lived (60 seconds) and can be used only once.
// The actual token bytes are never stored; only SHA-256(token) is persisted.
type MagicLinkToken struct {
	// ID is a 26-char ULID (Crockford base32).
	ID string `gorm:"type:char(26);primaryKey" json:"id"`

	// ApplicationInstallID references the application installation this token is for.
	ApplicationInstallID string `gorm:"type:char(26);not null;index:idx_application_install_id" json:"application_install_id"`

	// PanelUserID is the operator who minted the token. NOT NULL —
	// the mint endpoint requires a Kratos session, so this always
	// resolves to a real users.id. No FK because we want outstanding
	// tokens to survive operator deletion (the token is still valid;
	// only the operator's panel session is gone).
	PanelUserID string `gorm:"type:char(26);not null" json:"panel_user_id"`

	// TokenHash is the lowercase hex SHA-256 of the raw token.
	// This is what the /magic-link/verify endpoint checks.
	TokenHash string `gorm:"type:char(64);not null;uniqueIndex:uniq_token_hash" json:"token_hash"`

	// ExpiresAt is when this token becomes invalid (60 seconds from creation + skew tolerance).
	ExpiresAt time.Time `gorm:"type:datetime(6);not null;index:idx_expires_at" json:"expires_at"`

	// UsedAt is when this token was consumed. Null if unused.
	UsedAt *time.Time `gorm:"type:datetime(6)" json:"used_at"`

	// CreatedAt is the token creation timestamp.
	CreatedAt time.Time `gorm:"type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"created_at"`
}

// TableName pins the table name for GORM.
func (MagicLinkToken) TableName() string { return "magic_link_tokens" }

// Used returns true if this token has been consumed.
func (m *MagicLinkToken) Used() bool {
	return m.UsedAt != nil
}

// IsExpiredAt returns true if the token is past its TTL at the
// caller's `now`, with `skewTolerance` of slack on the *backwards*
// side. Concretely: a token is still valid if `now <= expires_at +
// skewTolerance`. Per ADR-0039 §10 the validator passes 10s here so
// a WordPress host whose clock runs ahead of the panel's by a few
// seconds doesn't see freshly-minted tokens as already-expired.
//
// Forward direction is unguarded: a clock running behind on the
// validator side just makes tokens live slightly longer, which is
// acceptable for a 60s TTL.
func (m *MagicLinkToken) IsExpiredAt(now time.Time, skewTolerance time.Duration) bool {
	return now.After(m.ExpiresAt.Add(skewTolerance))
}
