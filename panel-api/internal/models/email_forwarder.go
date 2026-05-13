package models

import "time"

// EmailForwarder represents a mail forwarder (either an alias or external forward).
// Stalwart integration: x:UserAccount.aliases + x:SieveUserScript.
// Jabali is truth; reconciler converges to Stalwart on every tick.
//
// Type 'alias': Mail to local_part@domain is delivered to mailbox_id.
// Type 'external': Mail to local_part@domain is forwarded to target (an outside email).
type EmailForwarder struct {
	ID        string    `gorm:"type:char(26);primaryKey" json:"id"`
	MailboxID *string   `gorm:"type:char(26);index:ix_email_forwarders_mailbox" json:"mailbox_id,omitempty"`
	DomainID  string    `gorm:"type:char(26);not null;index:ix_email_forwarders_domain" json:"domain_id"`
	Type      string    `gorm:"type:enum('alias','external');not null" json:"type"`
	LocalPart *string   `gorm:"type:varchar(64)" json:"local_part"` // NULL for type='external'
	Target    string    `gorm:"type:varchar(255);not null" json:"target"`
	Enabled   bool      `gorm:"type:tinyint(1);not null;default:1" json:"enabled"`
	ManagedBy string    `gorm:"type:varchar(16);default:'m6.5'" json:"managed_by"`
	CreatedAt time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (EmailForwarder) TableName() string { return "email_forwarders" }
