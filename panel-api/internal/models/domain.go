package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// PageRedirect is one entry in a domain's page_redirects JSON array.
// Source is a path starting with "/"; Destination is a full URL; Type
// is a redirect HTTP status code as a short string ("301"/"302"/"307"/"308").
// Active is optional; nil means true (backwards compatibility for rows stored without this field).
// Wildcard matches all paths starting with Source (prefix match with captured remainder).
// NOTE: PageRedirects is stored as JSON in a DB column. No schema migration needed —
// GORM serializes the full struct. When rows stored without Active field are Unmarshaled,
// Active becomes nil and must be treated as true for backwards compatibility.
type PageRedirect struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Type        string `json:"type"`
	Active      *bool  `json:"active,omitempty"`
	Wildcard    bool   `json:"wildcard,omitempty"`
}

// PageRedirects implements driver.Valuer / sql.Scanner so GORM can
// persist it to a JSON column. An empty slice round-trips as SQL NULL
// (keeps the column genuinely empty, not a literal "[]").
type PageRedirects []PageRedirect

func (p PageRedirects) Value() (driver.Value, error) {
	if len(p) == 0 {
		return nil, nil
	}
	return json.Marshal(p)
}

func (p *PageRedirects) Scan(src any) error {
	if src == nil {
		*p = nil
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("PageRedirects.Scan: unsupported type %T", src)
	}
	if len(b) == 0 {
		*p = nil
		return nil
	}
	return json.Unmarshal(b, p)
}

// NginxRule is one entry in a domain's nginx_rules JSON array. Rules
// are discriminated by the Type field; each type consumes a different
// subset of the remaining fields. Keeping a single flat struct (vs
// interface/any) avoids an unmarshalling foot-gun and matches how
// PageRedirect is structured.
type NginxRule struct {
	Type string `json:"type"`

	// custom_header
	Name   string `json:"name,omitempty"`
	Value  string `json:"value,omitempty"`
	Always *bool  `json:"always,omitempty"`

	// rewrite
	Pattern     string `json:"pattern,omitempty"`
	Replacement string `json:"replacement,omitempty"`
	Flag        string `json:"flag,omitempty"` // last|break|redirect|permanent

	// proxy_pass
	Target string `json:"target,omitempty"`

	// proxy_pass, ip_access
	Path string `json:"path,omitempty"`

	// ip_access
	Mode string   `json:"mode,omitempty"` // allow_list | deny_list
	IPs  []string `json:"ips,omitempty"`

	// max_upload_size
	Size string `json:"size,omitempty"`
}

// NginxRules implements driver.Valuer / sql.Scanner so GORM can
// persist it to a JSON column. An empty slice round-trips as SQL NULL
// (keeps the column genuinely empty, not a literal "[]").
type NginxRules []NginxRule

func (n NginxRules) Value() (driver.Value, error) {
	if len(n) == 0 {
		return nil, nil
	}
	return json.Marshal(n)
}

func (n *NginxRules) Scan(src any) error {
	if src == nil {
		*n = nil
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("NginxRules.Scan: unsupported type %T", src)
	}
	if len(b) == 0 {
		*n = nil
		return nil
	}
	return json.Unmarshal(b, n)
}

// Domain represents a hosted domain bound to a user account. Each domain
// gets an nginx vhost config managed by the agent and a document root
// under the user's home directory.
type Domain struct {
	ID     string `gorm:"type:char(26);primaryKey" json:"id"`
	UserID string `gorm:"type:char(26);not null;index:ix_domains_user_id" json:"user_id"`

	// Name is the fully qualified domain name (e.g. "example.com").
	// Unique across the entire panel — two users can't host the same domain.
	Name string `gorm:"type:varchar(255);uniqueIndex:ux_domains_name;not null" json:"name"`

	// DocRoot is the filesystem path served by nginx. Defaults to
	// /home/<username>/public_html/<domain> at creation time.
	DocRoot string `gorm:"type:varchar(512);not null;default:''" json:"doc_root"`

	// IsEnabled controls whether the nginx vhost symlink exists in
	// sites-enabled. Disabled domains still have their config on disk.
	IsEnabled bool `gorm:"type:tinyint(1);not null;default:1" json:"is_enabled"`

	// NginxCustomDirectives holds operator-provided nginx config snippets
	// injected into the server block. Validated before save — dangerous
	// directives (proxy_pass, lua_*) are rejected.
	NginxCustomDirectives *string `gorm:"type:text" json:"nginx_custom_directives,omitempty"`

	// RedirectAllTo, when non-nil, is a URL that every request to this
	// domain is redirected to (whole-domain redirect). Supersedes the
	// docroot and any PageRedirects. Must be an absolute http(s) URL.
	RedirectAllTo *string `gorm:"type:varchar(2048)" json:"redirect_all_to,omitempty"`

	// RedirectAllType is the HTTP status code for RedirectAllTo, as a
	// short string: "301", "302", "307", or "308". Required when
	// RedirectAllTo is set. Defaults to "301" when writing from the UI.
	RedirectAllType *string `gorm:"type:varchar(8)" json:"redirect_all_type,omitempty"`

	// PageRedirects is a JSON array of per-path redirects applied when
	// RedirectAllTo is nil. Each entry has a source path, destination
	// URL, and HTTP status code. Supports Active toggle (nil = true for
	// backwards compat) and Wildcard prefix matching (v2 feature).
	PageRedirects PageRedirects `gorm:"type:json" json:"page_redirects,omitempty"`

	// NginxRules is a JSON array of typed rule-builder entries that
	// compile to nginx directives server-side (see internal/nginxrules).
	// Separate from NginxCustomDirectives (which holds raw user snippets)
	// and from PageRedirects (which has its own dedicated UI).
	NginxRules NginxRules `gorm:"type:json" json:"nginx_rules,omitempty"`

	// IndexPriority picks which file(s) nginx serves as the default
	// directory index. Enum values (html_first/php_first/html_only/
	// php_only/full) map to nginx `index` directives in the agent.
	IndexPriority string `gorm:"type:varchar(32);not null;default:'html_first'" json:"index_priority"`

	// SSLEnabled tracks whether ACME certificate provisioning is active for this domain.
	// When true, the reconciler attempts to issue or renew a cert; when false,
	// the cert (if any) is not updated but may remain installed.
	SSLEnabled bool `gorm:"type:tinyint(1);not null;default:1" json:"ssl_enabled"`

	// SSLState is a computed field (not persisted) that represents the actual SSL
	// certificate state. Values: "active_le" (valid LE cert), "self_signed", "pending",
	// "failed", "off". Populated by the repository on List/ListByUserID.
	SSLState string `gorm:"-" json:"ssl_state,omitempty"`

	CreatedAt time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (Domain) TableName() string { return "domains" }
