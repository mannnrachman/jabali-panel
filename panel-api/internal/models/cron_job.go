package models

import "time"

// CronJob represents a user-scheduled cron job.
type CronJob struct {
	ID            string     `gorm:"type:char(26);primaryKey" json:"id"`
	UserID        string     `gorm:"type:char(26);not null" json:"user_id"`
	Name          string     `gorm:"type:varchar(100);not null" json:"name"`
	Command       string     `gorm:"type:varchar(1024);not null" json:"command"`
	Schedule      string     `gorm:"type:varchar(100);not null" json:"schedule"`
	Enabled       bool       `gorm:"type:tinyint(1);not null;default:1" json:"enabled"`
	LastRunAt     *time.Time `gorm:"type:timestamp;null" json:"last_run_at"`
	LastExitCode  *int       `gorm:"type:int;null" json:"last_exit_code"`
	LastError     *string    `gorm:"type:varchar(1024);null" json:"last_error"`
	CreatedAt     time.Time  `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP;onUpdate:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (CronJob) TableName() string { return "cron_jobs" }
