package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// AutomationToken — M44 HMAC-signed bearer token for external
// automations. Only `id`, `name`, `scopes`, and audit-trail fields
// are visible to listing endpoints; `secret_enc` is decrypted by the
// HMAC verify middleware on every request and never leaves the
// process. Plaintext secret is returned once at mint time.
type AutomationToken struct {
	ID          string         `gorm:"column:id;primaryKey;type:char(26)" json:"id"`
	Name        string         `gorm:"column:name;type:varchar(100);not null;uniqueIndex:uq_automation_tokens_name" json:"name"`
	Scopes      AutomationScopes `gorm:"column:scopes_json;type:json;not null" json:"scopes"`
	SecretEnc   []byte         `gorm:"column:secret_enc;type:varbinary(255);not null" json:"-"`
	CreatedBy   *string        `gorm:"column:created_by;type:char(26)" json:"created_by,omitempty"`
	CreatedAt   time.Time      `gorm:"column:created_at;type:datetime(6);not null" json:"created_at"`
	LastUsedAt  *time.Time     `gorm:"column:last_used_at;type:datetime(6)" json:"last_used_at,omitempty"`
	LastUsedIP  *string        `gorm:"column:last_used_ip;type:varchar(45)" json:"last_used_ip,omitempty"`
	RevokedAt   *time.Time     `gorm:"column:revoked_at;type:datetime(6)" json:"revoked_at,omitempty"`
}

func (AutomationToken) TableName() string { return "automation_tokens" }

// AutomationScopes is a typed wrapper around []string with GORM
// JSON serialisation. Keeps the scope list explicit at the struct
// level while letting MariaDB store it as JSON for ad-hoc admin
// queries.
type AutomationScopes []string

// Scan implements the sql.Scanner interface.
func (s *AutomationScopes) Scan(src any) error {
	if src == nil {
		*s = nil
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("automation_scopes: unsupported scan type")
	}
	if len(b) == 0 {
		*s = nil
		return nil
	}
	return json.Unmarshal(b, s)
}

// Value implements the driver.Valuer interface.
func (s AutomationScopes) Value() (driver.Value, error) {
	if s == nil {
		s = AutomationScopes{}
	}
	return json.Marshal(s)
}

// Has returns true when the scope grants the requested capability.
// "read:*" matches any "read:..." capability; an exact-match scope
// like "read:domains" matches its own capability and nothing else.
// Unknown wildcards (e.g. a typo "rea:*") never match anything.
func (s AutomationScopes) Has(want string) bool {
	for _, granted := range s {
		if granted == want {
			return true
		}
		// Wildcard support: "read:*" matches "read:<anything>".
		if len(granted) > 2 && granted[len(granted)-2:] == ":*" {
			prefix := granted[:len(granted)-1] // "read:"
			if len(want) >= len(prefix) && want[:len(prefix)] == prefix {
				return true
			}
		}
	}
	return false
}
