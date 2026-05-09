package models

import "time"

// DomainIPACL is one per-domain IP allow/deny entry (M36).
//
// `action` is either "allow" or "deny". Lower `priority` evaluated
// first by the reconciler when rendering nginx directives. Common
// patterns:
//   - whitelist mode: priority=0 allow 1.2.3.0/24 + priority=1 deny 0.0.0.0/0
//   - blacklist mode: priority=0 deny 5.6.7.8/32 + priority=1 allow 0.0.0.0/0
type DomainIPACL struct {
	ID        string    `gorm:"column:id;type:char(26);primaryKey" json:"id"`
	DomainID  string    `gorm:"column:domain_id;type:char(26);not null;index:idx_domain_ip_acls_domain,priority:1" json:"domain_id"`
	CIDR      string    `gorm:"column:cidr;type:varchar(64);not null" json:"cidr"`
	Action    string    `gorm:"column:action;type:varchar(8);not null" json:"action"`
	Priority  int       `gorm:"column:priority;type:int;not null;default:0;index:idx_domain_ip_acls_domain,priority:2" json:"priority"`
	Comment   string    `gorm:"column:comment;type:varchar(200);not null;default:''" json:"comment"`
	CreatedAt time.Time `gorm:"column:created_at;type:datetime(6);not null" json:"created_at"`
}

func (DomainIPACL) TableName() string { return "domain_ip_acls" }
