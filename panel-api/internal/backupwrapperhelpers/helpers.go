// Package backupwrapperhelpers translates a panel-api BackupDestination
// row into the inputs the internal/backup wrapper needs (URL, extra
// `-o key=value` flags, env file path). Lives here rather than inside
// internal/backup to keep that package free of GORM models.
package backupwrapperhelpers

import (
	internalbackup "git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// ResticOptionsFor returns the `-o key=value` flag bodies (without the
// `-o` prefix; callers prepend) for one destination. Currently only
// SFTP needs overrides; other backends return an empty slice.
func ResticOptionsFor(d *models.BackupDestination) []string {
	if d == nil {
		return nil
	}
	opts := d.ExtraOptionsTyped()
	if d.Kind == models.BackupDestinationKindSFTP && opts.SFTP != nil {
		s := opts.SFTP
		flag := internalbackup.SFTPCommandFlag(internalbackup.SFTPInputs{
			Host:    s.Host,
			User:    s.User,
			Port:    s.Port,
			Path:    s.Path,
			Auth:    s.Auth,
			KeyPath: s.KeyPath,
		})
		if flag != "" {
			return []string{flag}
		}
	}
	return nil
}
