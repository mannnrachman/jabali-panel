package models

import "time"

// DatabaseUserGrant associates a database user with a database and specifies access level.
type DatabaseUserGrant struct {
	ID             string `gorm:"type:char(26);primaryKey" json:"id"`
	DatabaseID     string `gorm:"type:char(26);not null" json:"database_id"`
	DatabaseUserID string `gorm:"type:char(26);not null" json:"database_user_id"`
	GrantLevel     string `gorm:"type:enum('rw','ro');not null" json:"grant_level"`
	CreatedAt      time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (DatabaseUserGrant) TableName() string { return "database_user_grants" }
