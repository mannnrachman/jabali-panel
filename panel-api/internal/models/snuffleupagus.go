// Package models — Snuffleupagus state + incident + rule-override rows.
// M41, ADR-0088.
package models

import "time"

type SnuffleupagusMode string

const (
	SnuffleupagusModeOff        SnuffleupagusMode = "off"
	SnuffleupagusModeSimulation SnuffleupagusMode = "simulation"
	SnuffleupagusModeEnforce    SnuffleupagusMode = "enforce"
)

type SnuffleupagusState struct {
	ID                int8              `gorm:"primaryKey;default:1"     json:"-"`
	Mode              SnuffleupagusMode `gorm:"type:enum('off','simulation','enforce');not null;default:'off'" json:"mode"`
	LastAppliedAt     *time.Time        `gorm:"type:datetime(6)"          json:"last_applied_at,omitempty"`
	LastAppliedSha256 *string           `gorm:"type:char(64)"             json:"last_applied_sha256,omitempty"`
}

func (SnuffleupagusState) TableName() string { return "snuffleupagus_state" }

type SnuffleupagusRuleOverride struct {
	RuleName     string    `gorm:"primaryKey;type:varchar(128)" json:"rule_name"`
	Enabled      bool      `gorm:"type:tinyint(1);not null;default:1" json:"enabled"`
	Reason       *string   `gorm:"type:varchar(512)"           json:"reason,omitempty"`
	SetByUserID  *string   `gorm:"type:char(26)"               json:"set_by_user_id,omitempty"`
	SetAt        time.Time `gorm:"type:datetime(6);not null;default:current_timestamp(6)" json:"set_at"`
}

func (SnuffleupagusRuleOverride) TableName() string { return "snuffleupagus_rule_overrides" }

type SnuffleupagusAction string

const (
	SnuffleupagusActionLog            SnuffleupagusAction = "log"
	SnuffleupagusActionBlock          SnuffleupagusAction = "block"
	SnuffleupagusActionSimulatedBlock SnuffleupagusAction = "simulated_block"
)

type SnuffleupagusIncident struct {
	ID         int64               `gorm:"primaryKey;autoIncrement" json:"id"`
	Ts         time.Time           `gorm:"type:datetime(6);not null;index:ix_snuf_inc_ts" json:"ts"`
	RuleName   string              `gorm:"type:varchar(128);not null;index:ix_snuf_inc_rule" json:"rule_name"`
	Action     SnuffleupagusAction `gorm:"type:enum('log','block','simulated_block');not null" json:"action"`
	SourceIP   []byte              `gorm:"type:varbinary(16)" json:"-"`
	RequestURI *string             `gorm:"type:varchar(2048)" json:"request_uri,omitempty"`
	PhpVersion *string             `gorm:"type:varchar(8);column:php_version" json:"php_version,omitempty"`
	DomainID   *string             `gorm:"type:char(26);column:domain_id;index:ix_snuf_inc_domain" json:"domain_id,omitempty"`
	Raw        *string             `gorm:"type:text" json:"raw,omitempty"`
}

func (SnuffleupagusIncident) TableName() string { return "snuffleupagus_incidents" }
