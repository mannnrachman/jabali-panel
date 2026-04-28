package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ManifestSchemaVersion is the wire-contract version. Bumped only when
// a non-additive change ships; restore refuses unknown versions.
const ManifestSchemaVersion = 1

// ErrUnsupportedSchema is returned by ManifestFromBytes when the
// schema_version field is unknown.
var ErrUnsupportedSchema = errors.New("backup manifest: unsupported schema_version")

// ErrManifestMalformed is returned for any other parse failure.
var ErrManifestMalformed = errors.New("backup manifest: malformed payload")

// AccountManifest is the per-account_backup manifest. Stored as a
// stdin-piped restic snapshot tagged stage=manifest. Restore reads
// this snapshot first, validates schema_version, then resolves the
// per-stage snapshot IDs by `--tag job-id=<manifest.job_id>`.
type AccountManifest struct {
	SchemaVersion int               `json:"schema_version"`
	Kind          string            `json:"kind"` // "account_backup"
	JobID         string            `json:"job_id"`
	CreatedAt     time.Time         `json:"created_at"`
	Source        ManifestSource    `json:"source"`
	User          ManifestUser      `json:"user"`
	Restic        ManifestRestic    `json:"restic"`
	Stages        []ManifestStage   `json:"stages"`
	Warnings      []string          `json:"warnings,omitempty"`
}

// SystemManifest is the per-system_backup manifest. Stored as a
// stdin-piped restic snapshot tagged kind=system_backup stage=manifest.
type SystemManifest struct {
	SchemaVersion      int                 `json:"schema_version"`
	Kind               string              `json:"kind"` // "system_backup"
	JobID              string              `json:"job_id"`
	CreatedAt          time.Time           `json:"created_at"`
	Source             ManifestSource      `json:"source"`
	Restic             ManifestRestic      `json:"restic"`
	Stages             []ManifestStage     `json:"stages"`
	LinkedAccountJobs  []string            `json:"linked_account_jobs,omitempty"`
	Warnings           []string            `json:"warnings,omitempty"`
}

// ManifestSource captures where the snapshot came from. `panel_sha` is
// the git SHA at build time so two-VM bare-metal recovery can detect
// version drift before applying.
type ManifestSource struct {
	Hostname     string `json:"hostname"`
	PanelSHA     string `json:"panel_sha"`
	PanelVersion string `json:"panel_version,omitempty"`
}

// ManifestUser is the per-account user pointer in an account_backup
// manifest. Restore uses ID for lookup and Username for create-on-miss
// fallback.
type ManifestUser struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Email        string `json:"email,omitempty"`
	UIDAtSource  uint32 `json:"uid_at_source,omitempty"`
	IsAdmin      bool   `json:"is_admin"`
}

// ManifestRestic carries restic-specific metadata. SnapshotID identifies
// the manifest snapshot itself (so the row can be queried directly);
// per-stage SnapshotID lives on each ManifestStage.
type ManifestRestic struct {
	SnapshotID     string `json:"snapshot_id,omitempty"`
	ParentSnapshot string `json:"parent_snapshot,omitempty"`
	BytesAdded     uint64 `json:"bytes_added,omitempty"`
	BytesTotal     uint64 `json:"bytes_total,omitempty"`
}

// ManifestStage is one entry in stages[]. Status is one of:
// "ok", "skipped", "failed". Warnings carries non-fatal stage-level
// notes (e.g. "stalwart_down" on the mail stage).
type ManifestStage struct {
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	Tag        string   `json:"tag,omitempty"`         // e.g. "stage=home"
	SnapshotID string   `json:"snapshot_id,omitempty"` // restic snap ID for this stage
	Items      []string `json:"items,omitempty"`       // dbs[], zones[], mailboxes[]
	Warnings   []string `json:"warnings,omitempty"`
	// BytesAdded/Total mirror restic's --json summary for the stage.
	// BytesAdded = new unique data after dedup; BytesTotal = logical
	// bytes scanned. Sum across stages = top-level ManifestRestic.
	BytesAdded uint64 `json:"bytes_added,omitempty"`
	BytesTotal uint64 `json:"bytes_total,omitempty"`
}

// Stage status values.
const (
	StageStatusOK      = "ok"
	StageStatusSkipped = "skipped"
	StageStatusFailed  = "failed"
)

// AccountManifestFromBytes parses an account-manifest payload. Refuses
// unknown schema versions before any field-level work.
func AccountManifestFromBytes(b []byte) (*AccountManifest, error) {
	var m AccountManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManifestMalformed, err)
	}
	if m.SchemaVersion != ManifestSchemaVersion {
		return nil, fmt.Errorf("%w: got %d want %d", ErrUnsupportedSchema, m.SchemaVersion, ManifestSchemaVersion)
	}
	if m.Kind != KindAccountBackup {
		return nil, fmt.Errorf("%w: kind %q is not account_backup", ErrManifestMalformed, m.Kind)
	}
	return &m, nil
}

// SystemManifestFromBytes parses a system-manifest payload.
func SystemManifestFromBytes(b []byte) (*SystemManifest, error) {
	var m SystemManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManifestMalformed, err)
	}
	if m.SchemaVersion != ManifestSchemaVersion {
		return nil, fmt.Errorf("%w: got %d want %d", ErrUnsupportedSchema, m.SchemaVersion, ManifestSchemaVersion)
	}
	if m.Kind != KindSystemBackup {
		return nil, fmt.Errorf("%w: kind %q is not system_backup", ErrManifestMalformed, m.Kind)
	}
	return &m, nil
}

// ToBytes serializes a manifest with stable indentation (2-space) so
// golden-file tests reproduce.
func (m *AccountManifest) ToBytes() ([]byte, error) {
	if m.SchemaVersion == 0 {
		m.SchemaVersion = ManifestSchemaVersion
	}
	if m.Kind == "" {
		m.Kind = KindAccountBackup
	}
	return json.MarshalIndent(m, "", "  ")
}

// ToBytes serializes a system manifest.
func (m *SystemManifest) ToBytes() ([]byte, error) {
	if m.SchemaVersion == 0 {
		m.SchemaVersion = ManifestSchemaVersion
	}
	if m.Kind == "" {
		m.Kind = KindSystemBackup
	}
	return json.MarshalIndent(m, "", "  ")
}
