// Package models — M34 per-user PHP-FPM egress firewall schema.
// Two tables backing the per-user nftables policy + the user-facing
// access-request queue. See ADR-0084 + migrations 000100/000101.
package models

import (
	"encoding/json"
	"time"
)

// UserEgressState mirrors the ENUM in migration 000100.
const (
	UserEgressStateOff      = "off"
	UserEgressStateLearning = "learning"
	UserEgressStateEnforced = "enforced"
)

// UserEgressRequestStatus mirrors the ENUM in migration 000101.
const (
	UserEgressRequestStatusPending  = "pending"
	UserEgressRequestStatusApproved = "approved"
	UserEgressRequestStatusDenied   = "denied"
)

// UserEgressProtocol mirrors the ENUM in migration 000101.
const (
	UserEgressProtocolTCP = "tcp"
	UserEgressProtocolUDP = "udp"
)

// EgressDestination is one entry in user_egress_policies.allowed_extra.
// Port and Protocol are optional; an empty Port means "any port for the
// given protocol", and an empty Protocol means "tcp" by default. Comment
// is for the human auditor — never read by the nft template.
type EgressDestination struct {
	CIDR     string `json:"cidr"`
	Port     *int   `json:"port,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

// UserEgressPolicy is one row of user_egress_policies. AllowedExtra
// stays as raw JSON at the column level so concurrent writers do not
// stomp each other; handlers decode lazily via DecodeAllowedExtra.
type UserEgressPolicy struct {
	UserID            string          `gorm:"type:varchar(26);primaryKey"                       json:"user_id"`
	State             string          `gorm:"type:enum('off','learning','enforced');not null;default:'enforced';index:idx_user_egress_state" json:"state"`
	AllowedExtra      json.RawMessage `gorm:"type:json;not null"                                 json:"allowed_extra"`
	DropCount24h      uint64          `gorm:"column:drop_count_24h;type:bigint unsigned;not null;default:0;index:idx_user_egress_drops" json:"drop_count_24h"`
	DropCountAt       *time.Time      `gorm:"column:drop_count_at;type:timestamp"                json:"drop_count_at,omitempty"`
	LearningStartedAt *time.Time      `gorm:"column:learning_started_at;type:timestamp"          json:"learning_started_at,omitempty"`
	UpdatedAt         time.Time       `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"  json:"updated_at"`
	UpdatedBy         *string         `gorm:"type:varchar(26)"                                   json:"updated_by,omitempty"`
}

// TableName pins to migration 000100.
func (UserEgressPolicy) TableName() string { return "user_egress_policies" }

// DecodeAllowedExtra unmarshals the JSON column into a typed slice.
// Returns an empty slice (never nil) when the column is empty / null
// so callers can range over the result without nil checks.
func (p UserEgressPolicy) DecodeAllowedExtra() ([]EgressDestination, error) {
	if len(p.AllowedExtra) == 0 || string(p.AllowedExtra) == "null" {
		return []EgressDestination{}, nil
	}
	var out []EgressDestination
	if err := json.Unmarshal(p.AllowedExtra, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return []EgressDestination{}, nil
	}
	return out, nil
}

// UserEgressRequest is one row of user_egress_requests — a user-submitted
// destination request awaiting admin review. Approval flips status to
// 'approved' and the handler folds the destination into the user's
// allowed_extra list before the next reconciler tick re-renders nftables.
type UserEgressRequest struct {
	ID         string     `gorm:"type:varchar(26);primaryKey"                                  json:"id"`
	UserID     string     `gorm:"type:varchar(26);not null;index:idx_egress_req_user_status"   json:"user_id"`
	CIDR       string     `gorm:"column:cidr;type:varchar(43);not null"                        json:"cidr"`
	Port       *uint      `gorm:"type:int unsigned"                                            json:"port,omitempty"`
	Protocol   string     `gorm:"type:enum('tcp','udp');not null;default:'tcp'"                json:"protocol"`
	Reason     string     `gorm:"type:varchar(500);not null"                                   json:"reason"`
	Status     string     `gorm:"type:enum('pending','approved','denied');not null;default:'pending';index:idx_egress_req_status_created" json:"status"`
	ReviewedBy *string    `gorm:"type:varchar(26)"                                             json:"reviewed_by,omitempty"`
	DecidedAt  *time.Time `gorm:"type:timestamp"                                               json:"decided_at,omitempty"`
	CreatedAt  time.Time  `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"            json:"created_at"`
}

// TableName pins to migration 000101.
func (UserEgressRequest) TableName() string { return "user_egress_requests" }
