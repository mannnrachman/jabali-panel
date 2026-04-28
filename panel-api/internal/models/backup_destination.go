// M30.1 backup destinations (ADR-0078). Schema in migration 000086.
// Admin-managed remotes for restic copy. Credentials live in env files
// at /etc/jabali-panel/restic-remotes/<id>.env (root:root 0600); the
// DB carries only the file pointer.
package models

import "time"

const (
	BackupDestinationKindLocal = "local"
	BackupDestinationKindSFTP  = "sftp"
	BackupDestinationKindS3    = "s3"
	BackupDestinationKindB2    = "b2"
	BackupDestinationKindAzure = "azure"
	BackupDestinationKindGCS   = "gcs"
	BackupDestinationKindREST  = "rest"
)

// AllBackupDestinationKinds is the canonical list, used by REST validation
// and the admin UI dropdown. Order matters for the UI menu.
var AllBackupDestinationKinds = []string{
	BackupDestinationKindLocal,
	BackupDestinationKindSFTP,
	BackupDestinationKindS3,
	BackupDestinationKindB2,
	BackupDestinationKindAzure,
	BackupDestinationKindGCS,
	BackupDestinationKindREST,
}

type BackupDestination struct {
	ID             string    `gorm:"type:char(26);primaryKey" json:"id"`
	Name           string    `gorm:"type:varchar(64);not null;uniqueIndex:uniq_backup_dest_name" json:"name"`
	Kind           string    `gorm:"type:enum('local','sftp','s3','b2','azure','gcs','rest');not null" json:"kind"`
	URL            string    `gorm:"type:varchar(512);not null" json:"url"`
	CredentialsRef *string   `gorm:"type:varchar(255)" json:"credentials_ref,omitempty"`
	Enabled        bool      `gorm:"not null;default:1" json:"enabled"`
	CreatedAt      time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (BackupDestination) TableName() string { return "backup_destinations" }
