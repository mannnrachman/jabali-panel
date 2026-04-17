package models

import "time"

// PHPPool represents a PHP-FPM pool bound to a panel user.
// Each panel user gets exactly one pool per MVP constraint.
type PHPPool struct {
	ID                        string    `gorm:"type:char(26);primaryKey" json:"id"`
	UserID                    string    `gorm:"type:char(26);not null" json:"user_id"`
	PHPVersion                string    `gorm:"type:varchar(8);not null" json:"php_version"`
	PmMode                    string    `gorm:"type:varchar(16);not null;default:'ondemand'" json:"pm_mode"`
	PmMaxChildren             uint32    `gorm:"type:int unsigned;not null;default:20" json:"pm_max_children"`
	ProcessIdleTimeoutSeconds uint32    `gorm:"type:int unsigned;not null;default:60" json:"process_idle_timeout_seconds"`
	Status                    string    `gorm:"type:varchar(16);not null;default:'pending'" json:"status"`
	LastError                 *string   `gorm:"type:text" json:"last_error,omitempty"`
	CreatedAt                 time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt                 time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (PHPPool) TableName() string { return "php_pools" }
