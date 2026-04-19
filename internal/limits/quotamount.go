package limits

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// QuotaMountFor returns the filesystem mount point that contains the
// given path — the value that must be passed as the final positional
// argument to `setquota -u <user> <blocks> ... <mount>`.
//
// Why not `setquota -a`: on a host with multiple quota-enabled mounts
// (say `/home` is ext4-with-usrquota AND `/var` is ext4-with-usrquota
// because the admin is experimenting), `-a` hits BOTH filesystems.
// We want per-user hosting quotas to land exactly on the user-home
// mount, which here means walking up the path until we find a
// mountpoint in /proc/mounts.
//
// Caches nothing — callers can wrap in a sync.Once if they want, but
// the stat cost at startup is microseconds and keeping it stateless
// lets tests inject paths.
func QuotaMountFor(path string) (string, error) {
	return quotaMountForWithMounts(path, "/proc/mounts")
}

// quotaMountForWithMounts is the parameterized form used by tests.
// Reads the mount table from mountsFile and finds the longest prefix
// of path that's a mount point. Longest-prefix wins because /home on
// its own mount must beat / (which is always a prefix of /home).
func quotaMountForWithMounts(path, mountsFile string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("abs %q: %w", path, err)
	}
	// Normalize to /home not /home/ so prefix match below is stable.
	if abs != "/" {
		abs = strings.TrimRight(abs, "/")
	}

	f, err := os.Open(mountsFile)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", mountsFile, err)
	}
	defer f.Close()

	var best string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// /proc/mounts format: <device> <mountpoint> <fstype> <opts> <dump> <pass>
		// Fields can contain escaped spaces (\040) but for hosting hosts
		// we don't care — mount paths are /home, /var, /, etc.
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		mp := fields[1]
		// Longest-prefix match, treating mp="/" as the universal fallback.
		if mp == abs || mp == "/" || strings.HasPrefix(abs, mp+"/") {
			if len(mp) > len(best) {
				best = mp
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("scan %s: %w", mountsFile, err)
	}
	if best == "" {
		return "", fmt.Errorf("no mount point found containing %q", abs)
	}
	return best, nil
}
