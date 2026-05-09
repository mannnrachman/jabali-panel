package models

import "time"

// UserEgressDropSample is one tick's worth of M34 egress drops for
// one user. The reconciler.readUserEgressCounters tick computes
// `delta = current_packets - last_seen` per user; this row persists
// that delta so the admin Egress card can render a 24h sparkline.
type UserEgressDropSample struct {
	UserID string    `gorm:"column:user_id;type:char(26);primaryKey" json:"user_id"`
	At     time.Time `gorm:"column:at;type:datetime(6);primaryKey" json:"at"`
	Drops  uint64    `gorm:"column:drops;type:bigint unsigned;not null;default:0" json:"drops"`
}

func (UserEgressDropSample) TableName() string { return "user_egress_drop_samples" }
