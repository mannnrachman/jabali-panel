package models

import "time"

type SSHKey struct {
	ID          string    `gorm:"type:char(26);primaryKey" json:"id"`
	UserID      string    `gorm:"type:char(26);not null;index" json:"user_id"`
	Name        string    `gorm:"type:varchar(128);not null" json:"name"`
	PublicKey   string    `gorm:"type:text;not null" json:"public_key"`
	Fingerprint string    `gorm:"type:char(64);not null" json:"fingerprint"`
	CreatedAt   time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
}

func (SSHKey) TableName() string { return "ssh_keys" }
