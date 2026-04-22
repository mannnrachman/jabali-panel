package models

import (
	"net"
	"time"
)

// ManagedIP is a single entry in the admin-curated pool of IPs the
// panel can bind to domains. See ADR-0048 / plans/m24-ip-manager.md.
//
// The Address column is the canonical key (UNIQUE). Family is stored
// for indexed lookup but ALWAYS derived server-side from Address —
// never trust a client-sent family.
type ManagedIP struct {
	ID      uint64 `gorm:"primaryKey;autoIncrement;type:bigint unsigned"     json:"id"`
	Address string `gorm:"type:varchar(45);not null;uniqueIndex:uq_managed_ips_address" json:"address"`
	// Family is "ipv4" or "ipv6". Set by the repo on Create from
	// DeriveFamily(Address); the API layer never reads a client-supplied
	// family.
	Family string `gorm:"type:varchar(8);not null;index:idx_managed_ips_family" json:"family"`
	Label  string `gorm:"type:varchar(120);not null;default:''"             json:"label"`

	// IsDefault marks the per-family fallback IP. Mirrors
	// server_settings.public_ipv4 / public_ipv6 for the seeded primary;
	// can be changed via PATCH /admin/ips/:id (Step 2).
	IsDefault bool `gorm:"type:tinyint(1);not null;default:0" json:"is_default"`

	// IsBound is set by the agent flow once `ip addr add` confirms the
	// kernel binding. Pre-bound IPs (operator added via netplan before
	// adding to the panel) stay false; the agent won't re-bind them.
	IsBound bool `gorm:"type:tinyint(1);not null;default:0" json:"is_bound"`

	// IsUserSelectable exposes the IP in the user-shell domain picker.
	// Default false; admin opts in via PATCH /admin/ips/:id.
	IsUserSelectable bool `gorm:"type:tinyint(1);not null;default:0" json:"is_user_selectable"`

	// Degraded means the agent's rebind-on-start loop or the post-bind
	// connectivity probe (Step 3 R9 mitigation) flagged this IP as
	// non-functional. UI surfaces a warning; operator must intervene.
	Degraded bool `gorm:"type:tinyint(1);not null;default:0" json:"degraded"`

	CreatedAt time.Time `gorm:"type:datetime;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt time.Time `gorm:"type:datetime;not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (ManagedIP) TableName() string { return "managed_ips" }

// DeriveFamily classifies addr as "ipv4" or "ipv6" using
// net.ParseIP. Returns "" for inputs that don't parse — callers MUST
// reject empty results before persisting (the DB ENUM would too, but
// failing in Go yields a clearer error path).
//
// Implementation note: net.ParseIP("1.2.3.4").To4() returns non-nil
// for IPv4-mapped IPv6 forms too (::ffff:1.2.3.4); we explicitly check
// for ":" in the original string to keep "ipv6" answers for those
// inputs (admin who pastes a v4-mapped-v6 address probably means v6).
func DeriveFamily(addr string) string {
	ip := net.ParseIP(addr)
	if ip == nil {
		return ""
	}
	for i := 0; i < len(addr); i++ {
		if addr[i] == ':' {
			return "ipv6"
		}
	}
	if ip.To4() != nil {
		return "ipv4"
	}
	return "ipv6"
}
