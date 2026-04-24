package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// NotificationChannelKind enumerates the channel transports. Mirrors the
// ENUM in migration 000064.
const (
	NotificationChannelKindEmail   = "email"
	NotificationChannelKindSlack   = "slack"
	NotificationChannelKindDiscord = "discord"
	NotificationChannelKindNtfy    = "ntfy"
	NotificationChannelKindWebhook = "webhook"
	NotificationChannelKindWebpush = "webpush"
)

// NotificationChannel is a configured delivery target for system events.
// See ADR-0056 + plans/m14-notifications.md.
//
// Config is a per-kind blob (see NotificationChannelConfig). The API
// layer validates per-kind before persistence; the DB only enforces
// well-formed JSON.
type NotificationChannel struct {
	ID        string                    `gorm:"type:char(26);primaryKey" json:"id"`
	Name      string                    `gorm:"type:varchar(120);not null" json:"name"`
	Kind      string                    `gorm:"type:varchar(16);not null;index:idx_notification_channels_kind_enabled,priority:1" json:"kind"`
	Config    NotificationChannelConfig `gorm:"column:config_json;type:json;not null" json:"config"`
	Enabled   bool                      `gorm:"type:tinyint(1);not null;default:1;index:idx_notification_channels_kind_enabled,priority:2" json:"enabled"`
	CreatedAt time.Time                 `gorm:"type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"created_at"`
	UpdatedAt time.Time                 `gorm:"type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"updated_at"`
}

func (NotificationChannel) TableName() string { return "notification_channels" }

// NotificationChannelConfig holds the per-kind config blob. Fields are
// optional and interpreted per channel kind — see each sender in
// panel-api/internal/notif/senders/ (added in Step 3).
type NotificationChannelConfig struct {
	URL        string   `json:"url,omitempty"`
	Bearer     string   `json:"bearer,omitempty"`
	HMACSecret string   `json:"hmac_secret,omitempty"`
	Priority   int      `json:"priority,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	ToEmail    string   `json:"to_email,omitempty"`
	FromEmail  string   `json:"from_email,omitempty"`
}

func (c *NotificationChannelConfig) Scan(src any) error {
	if src == nil {
		*c = NotificationChannelConfig{}
		return nil
	}
	switch v := src.(type) {
	case []byte:
		return json.Unmarshal(v, c)
	case string:
		return json.Unmarshal([]byte(v), c)
	default:
		return errors.New("notification channel config: unexpected scan source type")
	}
}

func (c NotificationChannelConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}
