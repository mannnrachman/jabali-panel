package models

import "time"

// TerminalSession is a one-shot, IP+admin-bound token for opening a
// root web-terminal WebSocket (M45, ADR-0096). Mirrors LogAccessStream:
// minted by an authed admin POST, consumed exactly once on WS upgrade.
//
// Lifecycle: created (token, ExpiresAt ~60s) → UsedAt (WS upgrade,
// single-use) → StartedAt (PTY spawned) → EndedAt (PTY closed). The
// asciinema recording lives at CastPath for forensic replay.
type TerminalSession struct {
	ID     string `gorm:"type:char(26);primaryKey" json:"id"`
	UserID string `gorm:"type:char(26);not null;index:idx_terminal_user_id" json:"user_id"`

	// Token is a 256-bit base64url secret authenticating the WS upgrade.
	// Single-use: UsedAt is stamped on first connect and re-presentation
	// is rejected.
	Token string `gorm:"type:char(43);uniqueIndex:uniq_terminal_token;not null" json:"-"`

	// ClientIP is captured at mint and re-verified on WS upgrade so a
	// leaked token can't be replayed from another host.
	ClientIP string `gorm:"type:varchar(45);not null" json:"client_ip"`

	// ExpiresAt is the connect deadline (mint + ~60s). Unrelated to
	// session duration once connected (that's the agent idle/max timer).
	ExpiresAt time.Time `gorm:"type:timestamp;not null;index:idx_terminal_expires_at" json:"expires_at"`

	UsedAt    *time.Time `gorm:"type:datetime(6)" json:"used_at,omitempty"`
	StartedAt *time.Time `gorm:"type:datetime(6)" json:"started_at,omitempty"`
	EndedAt   *time.Time `gorm:"type:datetime(6)" json:"ended_at,omitempty"`

	CastPath  string    `gorm:"type:varchar(255)" json:"cast_path,omitempty"`
	CreatedAt time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
}

func (TerminalSession) TableName() string { return "terminal_sessions" }
