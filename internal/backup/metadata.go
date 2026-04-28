// Per-user metadata bundle written as a sidecar restic snapshot at
// stage=metadata. Captures everything that lives in panel-api MariaDB
// rows but isn't naturally part of /home, dumps, or mail data — e.g.
// the user's database users + grants, application install rows, ssh
// keys. Restore reads this back to recreate panel-side state at the
// same time the data stages get re-imported.
//
// This file lives in internal/backup so both panel-api (writer) and
// panel-agent (also writer for system-side counterpart) can share the
// schema. The producer side does the heavy lifting (panel-api sends
// the bundle in backup.create params); the agent only needs to know
// the JSON shape so it can write the snapshot.
package backup

// AccountMetadata is the JSON shape captured at stage=metadata for one
// account_backup. The schema_version + producer_panel_sha pair lets the
// restore-side reject bundles produced by an incompatible panel.
type AccountMetadata struct {
	SchemaVersion   int                       `json:"schema_version"`
	ProducerPanelSHA string                   `json:"producer_panel_sha,omitempty"`
	UserID          string                    `json:"user_id"`
	Username        string                    `json:"username"`
	Email           string                    `json:"email,omitempty"`
	DatabaseUsers   []MetadataDatabaseUser    `json:"database_users,omitempty"`
	AppInstalls     []MetadataAppInstall      `json:"app_installs,omitempty"`
}

// MetadataDatabaseUser persists a hosted MariaDB account belonging to
// the user, plus its grants. PasswordHash is the hash as stored in
// panel-api's database_users.password_hash column. Restore-side must
// treat the hash as opaque and write it back verbatim so user
// passwords keep working without a reset.
type MetadataDatabaseUser struct {
	ID           string                       `json:"id"`
	Username     string                       `json:"username"`
	PasswordHash string                       `json:"password_hash,omitempty"`
	CreatedAt    string                       `json:"created_at,omitempty"`
	Grants       []MetadataDatabaseUserGrant  `json:"grants,omitempty"`
}

// MetadataDatabaseUserGrant pairs a database user with the database
// it can access at the recorded grant level.
type MetadataDatabaseUserGrant struct {
	DatabaseID   string `json:"database_id"`
	DatabaseName string `json:"database_name,omitempty"`
	GrantLevel   string `json:"grant_level"`
	Privileges   string `json:"privileges,omitempty"`
}

// MetadataAppInstall is one row from application_installs. The struct
// intentionally mirrors the DB column names so a future restore can
// re-INSERT without a translation layer; new columns added to
// application_installs need a corresponding field here + a schema
// version bump.
type MetadataAppInstall struct {
	ID            string `json:"id"`
	UserID        string `json:"user_id"`
	DomainID      string `json:"domain_id"`
	DBID          string `json:"db_id,omitempty"`
	Version       string `json:"version,omitempty"`
	AdminUsername string `json:"admin_username"`
	AdminEmail    string `json:"admin_email"`
	Locale        string `json:"locale"`
	Status        string `json:"status"`
	UseWWW        bool   `json:"use_www"`
	Subdirectory  string `json:"subdirectory,omitempty"`
	AppType       string `json:"app_type"`
	CreatedAt     string `json:"created_at,omitempty"`
}

// MetadataSchemaVersion is the current AccountMetadata.schema_version.
// Bump on incompatible changes; restore validates strict equality.
const MetadataSchemaVersion = 1
