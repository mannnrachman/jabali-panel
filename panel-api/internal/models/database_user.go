package models

import "time"

// DatabaseUser is a user account created within a hosted database.
type DatabaseUser struct {
	ID           string `gorm:"type:char(26);primaryKey" json:"id"`
	UserID       string `gorm:"type:char(26);not null" json:"user_id"`
	Username     string `gorm:"type:varchar(64);not null" json:"username"`
	// M37 Phase 2 (migration 000112). Engine drives DB-level CREATE/
	// DROP/ALTER PASSWORD dispatch. Default mariadb for back-compat.
	Engine       string `gorm:"type:enum('mariadb','postgres');not null;default:'mariadb'" json:"engine"`
	PasswordHash string `gorm:"type:varchar(72);not null" json:"-"`
	CreatedAt    time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt    time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (DatabaseUser) TableName() string { return "database_users" }
