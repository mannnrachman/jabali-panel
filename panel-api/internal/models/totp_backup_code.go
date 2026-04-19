package models

import "time"

// TOTPBackupCode is one of the 10 single-use recovery codes generated when
// a user first enables 2FA. CodeHash is bcrypt(raw) so a DB leak never
// exposes redeemable values. A code is spent once UsedAt is non-nil.
type TOTPBackupCode struct {
	ID        string     `gorm:"type:char(26);primaryKey" json:"id"`
	UserID    string     `gorm:"type:char(26);not null;index:idx_totp_backup_user" json:"user_id"`
	CodeHash  string     `gorm:"type:varchar(72);not null" json:"-"`
	UsedAt    *time.Time `gorm:"type:datetime(6)" json:"used_at,omitempty"`
	CreatedAt time.Time  `gorm:"type:datetime(6);not null" json:"created_at"`
}

func (TOTPBackupCode) TableName() string { return "totp_backup_codes" }
