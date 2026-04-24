package models

import "time"

// WebhookEndpoint is the 1:1 retry/last-error sidecar for a
// NotificationChannel. Name survived from the early blueprint; it holds
// state for every sending channel, not just the generic webhook.
//
// The row is created lazily (first failure) or eagerly (first send) —
// the repository's Upsert method is idempotent. Deleting a channel
// cascades the endpoint row (FK ON DELETE CASCADE, migration 000064).
type WebhookEndpoint struct {
	ChannelID            string     `gorm:"type:char(26);primaryKey;column:channel_id" json:"channel_id"`
	LastSuccessAt        *time.Time `gorm:"type:datetime(6)" json:"last_success_at,omitempty"`
	LastError            string     `gorm:"type:text" json:"last_error,omitempty"`
	ConsecutiveFailures  int        `gorm:"type:int;not null;default:0" json:"consecutive_failures"`
	BackoffUntil         *time.Time `gorm:"type:datetime(6)" json:"backoff_until,omitempty"`
	UpdatedAt            time.Time  `gorm:"type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"updated_at"`
}

func (WebhookEndpoint) TableName() string { return "webhook_endpoints" }
