package commands

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// computeManagedDataDir returns the deterministic per-install data dir
// path for apps that need a writable directory OUTSIDE the docroot
// (Moodle's moodledata, Chamilo's app/upload moved out, GLPI's
// files/, etc.).
//
// Path: /home/<osUser>/<installID>-data/
//
// Rationale: deterministic from (osUser, installID) means the
// deleter doesn't have to query state to find the dir; it just
// recomputes from the same inputs. /home/<osUser> is already created
// + writable by the per-domain user so we don't need any new
// filesystem-prep step.
//
// installID is the panel install row UUID. Lower-cased to keep paths
// case-insensitive-friendly (some upstream installers stat() the path
// with different casing during sub-tasks).
func computeManagedDataDir(osUser, installID string) string {
	return filepath.Join("/home", osUser, strings.ToLower(installID)+"-data")
}

// ensureManagedDataDir creates the data dir if it doesn't already
// exist, chowns it to <osUser>:www-data, and chmods 0770 so:
//   - The per-domain user (running php-fpm + the install CLI) can
//     read/write everything in there.
//   - www-data (group bit) can read/write — needed if any app's
//     CLI scripts run under www-data instead of the domain user
//     (most don't, but some legacy ones do).
//   - World cannot peek (data dirs hold uploaded files, session
//     state, sometimes API keys).
//
// Idempotent: re-running on an existing dir succeeds and re-applies
// perms. Mostly useful for the "install failed half-way, retry"
// path.
func ensureManagedDataDir(ctx context.Context, osUser, installID string) (string, error) {
	dir := computeManagedDataDir(osUser, installID)

	if err := exec.CommandContext(ctx, "mkdir", "-p", dir).Run(); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := exec.CommandContext(ctx, "chown", osUser+":www-data", dir).Run(); err != nil {
		return "", fmt.Errorf("chown %s:www-data %s: %w", osUser, dir, err)
	}
	if err := exec.CommandContext(ctx, "chmod", "0770", dir).Run(); err != nil {
		return "", fmt.Errorf("chmod 0770 %s: %w", dir, err)
	}
	return dir, nil
}

// removeManagedDataDir deletes the per-install data dir. Called from
// app deleters that opt into the managed-data-dir contract. Best
// effort — a missing dir is treated as success (the install may have
// failed before the dir was created, or this is a re-delete).
func removeManagedDataDir(ctx context.Context, osUser, installID string) error {
	dir := computeManagedDataDir(osUser, installID)
	if err := exec.CommandContext(ctx, "rm", "-rf", dir).Run(); err != nil {
		return fmt.Errorf("rm -rf %s: %w", dir, err)
	}
	return nil
}
