package commands

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// phpVersionPattern matches the pinned-version file content. Matches
// strings like "8.3", "8.4", "8.5". Stricter than needed (real Sury
// versions go 7.4..8.5) but the upper bound drifts faster than we ship,
// so we just require "<digit>.<digit>" and trust the file source.
var phpVersionPattern = regexp.MustCompile(`^\d+\.\d+$`)

// phpCLIFor returns the PHP CLI binary name to use for osUser's
// installers. Reads the pinned version from
// /etc/jabali-panel/user-phpver/<user> and returns e.g. "php8.5". Falls
// back to "php" if unpinned or the file is unreadable — which is the
// correct behaviour on a fresh host before the pool manager has written
// anything, and matches the default CLI on systems without Sury's
// alternatives.
//
// Why this matters: extensions (intl, mbstring, gd, …) are installed
// per-PHP-version on Debian/Sury. A user pinned to 8.5 has intl there
// but the default /usr/bin/php on this host is 8.4 which doesn't. When
// a CMS installer runs `php maintenance/install.php` it resolves to
// /usr/bin/php (the default), misses intl, and aborts. Using the
// pinned binary aligns CLI execution with the FPM pool's extension
// set.
func phpCLIFor(osUser string) string {
	path := fmt.Sprintf("/etc/jabali-panel/user-phpver/%s", osUser)
	b, err := os.ReadFile(path) //nolint:gosec // path is built from validated osUser
	if err != nil {
		return "php"
	}
	v := strings.TrimSpace(string(b))
	if !phpVersionPattern.MatchString(v) {
		return "php"
	}
	return "php" + v
}
