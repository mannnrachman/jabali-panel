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

	// PHPPoolID optionally binds this domain to a PHP-FPM pool. When set, the
	// domain can execute PHP. When NULL, the domain is static (no PHP block in vhost).
	PHPPoolID *string `gorm:"type:char(26)" json:"php_pool_id,omitempty"`

	// PHP INI overrides: per-domain settings that override the pool default.
	// NULL means use the pool's default; empty string is invalid (disallowed by API validation).
	// These are rendered as fastcgi_param PHP_VALUE at the nginx layer.
	PHPMemoryLimit       *string `gorm:"type:varchar(16)" json:"php_memory_limit,omitempty"`
	PHPUploadMaxFilesize *string `gorm:"type:varchar(16)" json:"php_upload_max_filesize,omitempty"`
	PHPPostMaxSize       *string `gorm:"type:varchar(16)" json:"php_post_max_size,omitempty"`
	PHPMaxInputVars      *int    `gorm:"type:int unsigned" json:"php_max_input_vars,omitempty"`
	PHPMaxExecutionTime  *int    `gorm:"type:int unsigned" json:"php_max_execution_time,omitempty"`
	PHPMaxInputTime      *int    `gorm:"type:int unsigned" json:"php_max_input_time,omitempty"`

	// M18: per-domain HTTP rate/conn limits. Zero = unlimited (no
	// nginx directive emitted). RateLimitRPS is requests-per-SECOND
	// as seen by the reconciler; the vhost renderer converts to
	// nginx's native rate syntax (r/s or r/m) at render time and
	// uses `burst=rate*2 nodelay` to absorb short spikes.
	RateLimitRPS    uint32 `gorm:"type:int unsigned;not null;default:0" json:"rate_limit_rps"`
	ConnectionLimit uint32 `gorm:"type:int unsigned;not null;default:0" json:"connection_limit"`

	// M24: optional binding to specific IPv4/IPv6 in the managed_ips
	// pool. NULL means "use server primary" (managed_ips.is_default for
	// the family). FK enforced at DB level with ON DELETE RESTRICT;
	// the API translates restrict-violations into 409 with the affected
	// domains list. Pointers because nullable; uint64 because the
	// referenced managed_ips.id is BIGINT UNSIGNED auto-increment.
	ListenIPv4ID *uint64 `gorm:"column:listen_ipv4_id;type:bigint unsigned" json:"listen_ipv4_id,omitempty"`
	ListenIPv6ID *uint64 `gorm:"column:listen_ipv6_id;type:bigint unsigned" json:"listen_ipv6_id,omitempty"`

	// M6 email state (migration 000054). EmailEnabled drives whether the
	// reconciler sets up MX/SPF/DMARC zone records and whether the API
	// accepts mailbox creates. DkimSelector + DkimPublicKey mirror the
	// DNS TXT value published by the agent's domain.email_enable; the
	// private key stays on disk at /etc/jabali-panel/dkim/<name>.key
	// (ADR-0043). EmailEnabledAt is the last transition-to-enabled
	// timestamp — useful for operator audit and the reconciler to
	// re-publish DNS after a backup restore.
	EmailEnabled    bool       `gorm:"type:tinyint(1);not null;default:0" json:"email_enabled"`
	DkimSelector    *string    `gorm:"type:varchar(64)" json:"dkim_selector,omitempty"`
	DkimPublicKey   *string    `gorm:"type:text" json:"dkim_public_key,omitempty"`
	EmailEnabledAt  *time.Time `gorm:"type:datetime(6)" json:"email_enabled_at,omitempty"`

	// M26 ModSecurity per-domain toggle (migration 000064, ADR-0055).
	// When true, the per-vhost nginx template emits the modsecurity
	// include block (M26 Step 5). When false, the vhost has no modsec
	// directives and tenant traffic is not inspected. Default 0 — opt-in.
	ModSecEnabled bool `gorm:"type:tinyint(1);not null;default:0" json:"modsec_enabled"`

	// IsPanelPrimary marks the single domain row auto-registered for the
	// panel hostname (ADR-0048). Delete-protected at the repo and API
	// layer; surfaced in Settings → Email. At-most-one is enforced in
	// the Go repo layer (MarkPanelPrimary transaction) — NOT a SQL
	// UNIQUE (see migration 000057 for the reasoning).
	IsPanelPrimary bool `gorm:"type:tinyint(1);not null;default:0;index:ix_domains_panel_primary" json:"is_panel_primary"`

	// M6.5 Email Features: Catch-All, Disclaimer (per-domain fields).
	// CatchallTarget is the email address that receives unmatched domain mail.
	// Stalwart integration: x:Domain.catchAllAddress (ADR-0051).
	CatchallTarget *string `gorm:"type:varchar(255)" json:"catchall_target,omitempty"`

	// DisclaimerEnabled controls whether to append a text disclaimer to
	// outbound mail from this domain. DisclaimerText holds the text.
	// Stalwart integration: x:SieveSystemScript (ADR-0051).
	DisclaimerEnabled bool    `gorm:"type:tinyint(1);not null;default:0" json:"disclaimer_enabled"`
	DisclaimerText    *string `gorm:"type:text" json:"disclaimer_text,omitempty"`
	CreatedAt time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (Domain) TableName() string { return "domains" }
