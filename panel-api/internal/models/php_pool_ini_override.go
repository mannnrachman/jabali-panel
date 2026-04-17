package models

import "time"

// PHPPoolIniOverride represents a php.ini directive override for a PHP pool.
// Stored one-row-per-override; rendered as php_admin_value or php_admin_flag
// in the pool config file. Only allowlisted directives are permitted.
type PHPPoolIniOverride struct {
	ID        string    `gorm:"type:char(26);primaryKey" json:"id"`
	PoolID    string    `gorm:"type:char(26);not null" json:"pool_id"`
	Directive string    `gorm:"type:varchar(64);not null" json:"directive"`
	Value     string    `gorm:"type:varchar(255);not null" json:"value"`
	Kind      string    `gorm:"type:enum('value','flag');not null;default:'value'" json:"kind"`
	CreatedAt time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (PHPPoolIniOverride) TableName() string { return "php_pool_ini_overrides" }
