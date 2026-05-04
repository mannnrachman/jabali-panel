// Package backup — shared destination URL validation.
//
// Shared between panel-api REST handlers (admin form submit) and the
// CLI (`jabali backup destination create|update`). M30.1 / ADR-0078.
package backup

import (
	"errors"
	"fmt"
	"strings"
)

// Destination kind constants — duplicated from panel-api/internal/models
// because internal/backup is below panel-api in the dep graph (panel-api
// imports it). Keep these in sync; they exist as a tight allowlist used
// by ValidateURLForKind only.
const (
	KindLocal = "local"
	KindSFTP  = "sftp"
	KindS3    = "s3"
	KindB2    = "b2"
	KindAzure = "azure"
	KindGCS   = "gcs"
	KindREST  = "rest"
)

// ValidateURLForKind enforces the restic URL prefix per destination kind.
// Returns nil for empty kinds it doesn't recognise (caller is expected to
// validate the kind separately); returns an error for empty url or when
// the prefix is wrong for a known kind.
func ValidateURLForKind(kind, url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return errors.New("url required")
	}
	switch kind {
	case KindLocal:
		if !strings.HasPrefix(url, "/") {
			return errors.New("local URL must be an absolute path (e.g. /var/backups/restic)")
		}
	case KindSFTP:
		if !strings.HasPrefix(url, "sftp:") {
			return fmt.Errorf("sftp URL must start with 'sftp:' (e.g. sftp:user@host:/path)")
		}
	case KindS3:
		if !strings.HasPrefix(url, "s3:") {
			return errors.New("s3 URL must start with 's3:' (e.g. s3:s3.amazonaws.com/bucket)")
		}
	case KindB2:
		if !strings.HasPrefix(url, "b2:") {
			return errors.New("b2 URL must start with 'b2:' (e.g. b2:bucketname:/path)")
		}
	case KindAzure:
		if !strings.HasPrefix(url, "azure:") {
			return errors.New("azure URL must start with 'azure:' (e.g. azure:container/path)")
		}
	case KindGCS:
		if !strings.HasPrefix(url, "gs:") {
			return errors.New("gcs URL must start with 'gs:' (e.g. gs:bucket:/path)")
		}
	case KindREST:
		if !strings.HasPrefix(url, "rest:") {
			return errors.New("rest URL must start with 'rest:' (e.g. rest:https://user:pass@host:8000/repo)")
		}
	}
	return nil
}
