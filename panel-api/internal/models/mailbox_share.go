package models

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

// MailboxShare represents an ACL-like sharing relationship between two mailboxes.
// Stalwart integration: JMAP Mailbox/set + shareWith patch.
// Jabali is truth; reconciler converges to Stalwart.
type MailboxShare struct {
	ID                  string    `gorm:"type:char(26);primaryKey" json:"id"`
	OwnerMailboxID      string    `gorm:"type:char(26);not null;index:ix_mailbox_shares_owner" json:"owner_mailbox_id"`
	SharedWithMailboxID string    `gorm:"type:char(26);not null;index:ix_mailbox_shares_shared_with" json:"shared_with_mailbox_id"`
	Rights              Rights    `gorm:"type:json;not null" json:"rights"`
	ManagedBy           string    `gorm:"type:varchar(16);default:'m6.5'" json:"managed_by"`
	CreatedAt           time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
}

func (MailboxShare) TableName() string { return "mailbox_shares" }

// Rights encodes JMAP Mailbox access rights.
// For now, a simple struct; later expanded per RFC 8554.
type Rights struct {
	MayRead       bool `json:"mayRead,omitempty"`
	MayAddItems   bool `json:"mayAddItems,omitempty"`
	MayRemoveItems bool `json:"mayRemoveItems,omitempty"`
	MayCreateChild bool `json:"mayCreateChild,omitempty"`
	MayRename     bool `json:"mayRename,omitempty"`
	MayDelete     bool `json:"mayDelete,omitempty"`
	MayAdmin      bool `json:"mayAdmin,omitempty"`
	MaySubmit     bool `json:"maySubmit,omitempty"`
}

// Scan implements sql.Scanner for Rights.
func (r *Rights) Scan(src interface{}) error {
	switch v := src.(type) {
	case []byte:
		return json.Unmarshal(v, r)
	case string:
		return json.Unmarshal([]byte(v), r)
	case nil:
		return nil
	default:
		return nil
	}
}

// Value implements driver.Valuer for Rights.
func (r Rights) Value() (driver.Value, error) {
	return json.Marshal(r)
}
