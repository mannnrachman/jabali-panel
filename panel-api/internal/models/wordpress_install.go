package models

import "time"

// WordPressInstall represents a WordPress installation on a hosted domain.
type WordPressInstall struct {
	ID            string `gorm:"type:char(26);primaryKey" json:"id"`
	UserID        string `gorm:"type:char(26);not null" json:"user_id"`
	// (DomainID, Subdirectory) is the install's identity on disk. The unique
	// index spans both columns so a domain can host multiple installs as
	// long as each lives at a distinct subdirectory ("" = docroot install).
	DomainID      string `gorm:"type:char(26);not null;uniqueIndex:uniq_wpinstalls_domain_subdir,priority:1" json:"domain_id"`
	DBID          string `gorm:"type:char(26);not null;column:db_id" json:"db_id"`
	Version       *string `gorm:"type:varchar(32)" json:"version"`
	AdminUsername string `gorm:"type:varchar(60);not null" json:"admin_username"`
	AdminEmail    string `gorm:"type:varchar(320);not null" json:"admin_email"`
	Locale        string `gorm:"type:varchar(16);not null;default:'en_US'" json:"locale"`
	UseWWW        bool   `gorm:"type:boolean;not null;default:false" json:"use_www"`
	Subdirectory  string `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uniq_wpinstalls_domain_subdir,priority:2" json:"subdirectory"`
	Status        string `gorm:"type:varchar(16);not null;default:'pending'" json:"status"`
	LastError     string `gorm:"type:varchar(1024);not null;default:''" json:"last_error"`
	CreatedAt     time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt     time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (WordPressInstall) TableName() string { return "wordpress_installs" }
