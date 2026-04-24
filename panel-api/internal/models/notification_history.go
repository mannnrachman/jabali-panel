package models

import "time"

// NotificationSeverity enumerates the severity levels; mirrors the
// ENUM in migration 000064. Used both for routing (some channels only
// take error+ events) and for rendering in the bell dropdown.
const (
	NotificationSeverityInfo     = "info"
	NotificationSeverityWarning  = "warning"
	NotificationSeverityError    = "error"
	NotificationSeverityCritical = "critical"
)

// NotificationOutcome tracks the dispatcher lifecycle for a single
// history row. Pending rows are still in-flight or waiting for retry;
// sent/failed/skipped are terminal.
const (
	NotificationOutcomePending = "pending"
	NotificationOutcomeSent    = "sent"
	NotificationOutcomeFailed  = "failed"
	NotificationOutcomeSkipped = "skipped"
)

// NotificationHistory is the per-attempt audit row. One row per
// (event, channel) combination; channel_id is NULL for in-app-bell-only
// deliveries. user_id is set for per-user deliveries (web push,
// bell-read tracking) and NULL for broadcast rows.
//
// Indexes match the expected queries:
//   * UI bell:   WHERE user_id=? AND read_at IS NULL ORDER BY created_at DESC
//   * Dispatch: WHERE event_kind=? AND created_at >= ? (recent firing)
type NotificationHistory struct {
	ID           string     `gorm:"type:char(26);primaryKey" json:"id"`
	ChannelID    *string    `gorm:"type:char(26);column:channel_id" json:"channel_id,omitempty"`
	EventKind    string     `gorm:"type:varchar(60);not null;index:idx_notification_history_event_created,priority:1" json:"event_kind"`
	Severity     string     `gorm:"type:varchar(16);not null" json:"severity"`
	Title        string     `gorm:"type:varchar(200);not null" json:"title"`
	Body         string     `gorm:"type:text;not null" json:"body"`
	Deeplink     string     `gorm:"type:varchar(500)" json:"deeplink,omitempty"`
	Outcome      string     `gorm:"type:varchar(16);not null;default:'pending'" json:"outcome"`
	RetryCount   int        `gorm:"type:int;not null;default:0" json:"retry_count"`
	ErrorMessage string     `gorm:"type:text" json:"error_message,omitempty"`
	ReadAt       *time.Time `gorm:"type:datetime(6);index:idx_notification_history_user_read,priority:2" json:"read_at,omitempty"`
	UserID       *string    `gorm:"type:char(26);column:user_id;index:idx_notification_history_user_read,priority:1" json:"user_id,omitempty"`
	CreatedAt    time.Time  `gorm:"type:datetime(6);not null;default:CURRENT_TIMESTAMP(6);index:idx_notification_history_event_created,priority:2" json:"created_at"`
	UpdatedAt    time.Time  `gorm:"type:datetime(6);not null;default:CURRENT_TIMESTAMP(6)" json:"updated_at"`
}

func (NotificationHistory) TableName() string { return "notification_history" }
