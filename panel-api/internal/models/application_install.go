package models

import "time"

// ApplicationInstall is one installed app on a domain (or subdirectory of
// one). The (DomainID, Subdirectory, AppType) triplet is the install's
// identity on disk and is enforced unique by both the GORM tags below
// and migration 000046's composite unique key. WordPress is one
// AppType value among many; DokuWiki, MediaWiki, etc. share this
// table once their installer descriptors land in `internal/apps`.
type ApplicationInstall struct {
	ID     string `gorm:"type:char(26);primaryKey" json:"id"`
	UserID string `gorm:"type:char(26);not null"   json:"user_id"`

	// Composite unique on (domain_id, subdirectory, app_type). GORM
	// requires contiguous priorities (1, 2, 3) for a multi-column
	// uniqueIndex to materialise — get one wrong and the index silently
	// degrades to per-column uniques. Field declaration order matches
	// the pre-M19 model on purpose so GORM's INSERT column ordering
	// stays stable for the existing repository sqlmock tests; AppType
	// is appended last for the same reason.
	DomainID      string  `gorm:"type:char(26);not null;uniqueIndex:uniq_app_installs_domain_subdir_apptype,priority:1" json:"domain_id"`
	// DBID is nullable to support RequiresDB=false apps (DokuWiki,
	// Grav, Backdrop, ...). Migration 000048 relaxed the column;
	// callers writing this row must pass nil for flat-file apps.
	// Reads tolerate either NULL or "" — see DBIDValue() helper.
	DBID          *string `gorm:"type:char(26);column:db_id" json:"db_id"`
	Version       *string `gorm:"type:varchar(32)" json:"version"`
	AdminUsername string  `gorm:"type:varchar(60);not null" json:"admin_username"`
	AdminEmail    string  `gorm:"type:varchar(320);not null" json:"admin_email"`
	Locale        string  `gorm:"type:varchar(16);not null;default:'en_US'" json:"locale"`
	UseWWW        bool    `gorm:"type:boolean;not null;default:false" json:"use_www"`
	Subdirectory  string  `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uniq_app_installs_domain_subdir_apptype,priority:2" json:"subdirectory"`

	Status    string    `gorm:"type:varchar(16);not null;default:'pending'" json:"status"`
	LastError string    `gorm:"type:varchar(1024);not null;default:''" json:"last_error"`
	CreatedAt time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`

	// OIDCClientID is the Hydra OAuth 2 client provisioned for this
	// install (M16 Wave D). Nil for (a) pre-M16 rows, (b) apps whose
	// descriptor doesn't set OIDCCallbackPath, (c) the narrow window
	// between install-row insert and successful Hydra CreateClient.
	// The value is a ULID we generate panel-side so a compensating
	// DeleteClient on install-delete can target it without guessing.
	// Never serialised to clients — see the JSON tag.
	OIDCClientID *string `gorm:"type:char(40);column:oidc_client_id;uniqueIndex:uniq_app_installs_oidc_client_id" json:"-"`

	// OIDCClientSecretEnc is the AES-256-GCM envelope of the client
	// secret Hydra returned at CreateClient. Sealed with the same
	// sso.key used for phpMyAdmin shadow passwords. Shape:
	// nonce(12) || ciphertext || auth_tag(16). Nil when OIDCClientID
	// is nil. NEVER serialised (json:"-") and NEVER logged — callers
	// that need the plaintext call ssokey.Key.Open at the single
	// callsite that passes it to the agent.
	OIDCClientSecretEnc []byte `gorm:"type:varbinary(512);column:oidc_client_secret_enc" json:"-"`

	// AppType picks which app's installer the agent runs. Default
	// 'wordpress' so existing rows back-fill without a code change in
	// callers that still pre-date M19. Validated against the registry
	// (`internal/apps`) at API boundaries. Declared LAST so the column
	// is appended to existing INSERT/SELECT orderings rather than
	// reshuffling them.
	AppType string `gorm:"type:varchar(32);not null;default:'wordpress';uniqueIndex:uniq_app_installs_domain_subdir_apptype,priority:3" json:"app_type"`
}

// TableName pins the GORM mapping to the renamed table. Keep it explicit
// even though the default convention would resolve correctly — the name
// changed in 000046 and a typo here would silently route writes to the
// wrong (now non-existent) table.
func (ApplicationInstall) TableName() string { return "application_installs" }

// DBIDOr returns the string value of DBID, or "" when nil. Existing
// readers compare to "" (RequiresDB=false sentinel) and pass DBID as a
// string parameter; this accessor lets them keep doing both without
// every call site sprouting a nil-check after migration 000048 made
// the column nullable.
func (a *ApplicationInstall) DBIDOr() string {
	if a == nil || a.DBID == nil {
		return ""
	}
	return *a.DBID
}

// DBIDPtr converts a string DBID to the *string the model expects on
// write. "" maps to nil so RequiresDB=false apps insert NULL (the
// only value the FK accepts).
func DBIDPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// WordPressInstall is a transitional alias kept so the WordPress-specific
// API + agent paths compile during the M19 window without a sweeping
// rename. Step 3 introduces the generic /applications routes; subsequent
// steps replace the old WordPressInstall references one site at a time
// until M19.1 deletes this alias.
type WordPressInstall = ApplicationInstall
