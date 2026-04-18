package models

import "time"

// WordPressInstall represents a WordPress installation on a hosted domain.
type WordPressInstall struct {
	ID            string `gorm:"type:char(26);primaryKey" json:"id"`
	UserID        string `gorm:"type:char(26);not null" json:"user_id"`
	DomainID      string `gorm:"type:char(26);not null;uniqueIndex" json:"domain_id"`
	DBID          string `gorm:"type:char(26);not null;column:db_id" json:"db_id"`
	Version       *string `gorm:"type:varchar(32)" json:"version"`
	AdminUsername string `gorm:"type:varchar(60);not null" json:"admin_username"`
	AdminEmail    string `gorm:"type:varchar(320);not null" json:"admin_email"`
	Locale        string `gorm:"type:varchar(16);not null;default:'en_US'" json:"locale"`
	Status        string `gorm:"type:varchar(16);not null;default:'pending'" json:"status"`
	LastError     string `gorm:"type:varchar(1024);not null;default:''" json:"last_error"`
	CreatedAt     time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt     time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (WordPressInstall) TableName() string { return "wordpress_installs" }
