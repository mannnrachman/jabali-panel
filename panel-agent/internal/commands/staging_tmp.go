package commands

import (
	"fmt"
	"os"
)

// stagingRoot is where app-install handlers stage upstream tarballs
// before handing them off to a systemd-run --uid=<user> extract unit.
//
// Why not /tmp: jabali-agent runs with PrivateTmp=yes (install.sh),
// which sandboxes /tmp + /var/tmp into a per-service namespace. When
// the handler then spawns `systemd-run --uid=<user> tar ...`, the
// transient unit joins PID 1's mount namespace — the REAL host /tmp —
// not the agent's private one. An agent that wrote to /tmp/drupal-X/
// would see the tarball appear in its own private namespace while the
// extract unit saw an empty host /tmp → ENOENT.
//
// /var/lib/jabali-agent-staging/ is OUTSIDE the tmp sandbox, so the
// agent's writes and the transient unit's reads land on the same
// on-disk location. Keeping PrivateTmp=yes preserves symlink-race
// defense on any secrets the agent might ever stage in /tmp proper.
const stagingRoot = "/var/lib/jabali-agent-staging"

// stagingMkdirTemp creates a fresh dir under stagingRoot, ensuring the
// parent exists. Returns the absolute path; caller is responsible for
// os.RemoveAll on cleanup.
//
// Mode on the random dir is 0755 so per-user extract units can traverse
// it (they enter via systemd-run --uid=<user>). Tarball contents are
// public upstream source; no secrets are staged here.
func stagingMkdirTemp(prefix string) (string, error) {
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return "", fmt.Errorf("ensure staging root %s: %w", stagingRoot, err)
	}
	d, err := os.MkdirTemp(stagingRoot, prefix)
	if err != nil {
		return "", fmt.Errorf("mktemp under %s: %w", stagingRoot, err)
	}
	if err := os.Chmod(d, 0o755); err != nil {
		_ = os.RemoveAll(d)
		return "", fmt.Errorf("chmod staging dir: %w", err)
	}
	return d, nil
}
