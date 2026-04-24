package models

import "time"

// WebPushSubscription is a single admin's enrolled browser endpoint.
// See ADR-0057. One row per (user_id, endpoint); the endpoint URL is
// globally UNIQUE per the schema — a given browser has exactly one
// subscription regardless of which user enrolled it.
type WebPushSubscription struct {
	ID         string     `gorm:"type:char(26);primaryKey" json:"id"`
	UserID     string     `gorm:"type:char(26);not null;index:idx_webpush_subscriptions_user" json:"user_id"`
	Endpoint   string     `gorm:"type:varchar(500);not null;uniqueIndex:uq_webpush_subscriptions_endpoint" json:"endpoint"`
	P256dh     string     `gorm:"type:varchar(200);not null" json:"p256dh"`
	Auth       string     `gorm:"type:varchar(50);not null" json:"auth"`
	UserAgent  string     `gorm:"type:varchar(300)" json:"user_agent,omitempty"`
	CreatedAt  time.Time  `gorm:"type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"created_at"`
	LastUsedAt *time.Time `gorm:"type:datetime(6)" json:"last_used_at,omitempty"`
}

func (WebPushSubscription) TableName() string { return "webpush_subscriptions" }
