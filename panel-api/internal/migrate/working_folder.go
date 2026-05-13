// Package migrate — working_folder.go
//
// Resolves the operator-configured WorkingFolder from server_settings.
// Migrations + backups derive their on-disk staging paths from this so
// the operator can retarget to a larger disk (e.g. /mnt/storage) by
// flipping one setting + symlinking the legacy paths.
//
// Default: /var/lib/jabali. install.sh creates this dir at first boot
// + symlinks the legacy /var/lib/jabali-migrations + /var/lib/jabali-
// backups under it so existing data survives the schema bump.
package migrate

import (
	"context"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DefaultWorkingFolder mirrors the SQL DEFAULT on server_settings
// .working_folder. Callers should prefer ResolveWorkingFolder which
// reads the live row + falls back to this constant on miss.
const DefaultWorkingFolder = "/var/lib/jabali"

// ResolveWorkingFolder reads server_settings.working_folder. Returns
// the configured value when non-empty, else DefaultWorkingFolder. A
// nil settings repo (test wiring) returns the default — never panics.
func ResolveWorkingFolder(ctx context.Context, settings repository.ServerSettingsRepository) string {
	if settings == nil {
		return DefaultWorkingFolder
	}
	row, err := settings.Get(ctx)
	if err != nil || row == nil || row.WorkingFolder == "" {
		return DefaultWorkingFolder
	}
	return row.WorkingFolder
}

// MigrationsRoot returns <working_folder>/migrations — the parent
// directory each migration job's staging tree lives under.
func MigrationsRoot(ctx context.Context, settings repository.ServerSettingsRepository) string {
	return filepath.Join(ResolveWorkingFolder(ctx, settings), "migrations")
}

// BackupsRoot returns <working_folder>/backups — the restic repo +
// per-job log root.
func BackupsRoot(ctx context.Context, settings repository.ServerSettingsRepository) string {
	return filepath.Join(ResolveWorkingFolder(ctx, settings), "backups")
}
