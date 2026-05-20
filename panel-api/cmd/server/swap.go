package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ensureBuildSwap is the OOM-killed fallback for `jabali update`'s
// `build frontend` step. Small VMs (1-2 GB RAM, no swap) can't fit
// the vite bundler's working set even with NODE_OPTIONS heap caps;
// the kernel OOM-killer SIGKILLs node mid-bundle and the build fails
// with exit 137. Adding swap lets the build complete, slowly but
// reliably.
//
// Behaviour:
//
//   - If any swap is already active (/proc/swaps has >1 line), bail
//     with no error — the operator already provisioned swap, the OOM
//     was just oversize-RSS rather than no-swap. Caller will surface
//     the original error.
//   - Otherwise create /var/cache/jabali/build-swap (2 GiB) via
//     `fallocate` (fast on ext4/xfs), chmod 0600, mkswap, swapon.
//     Idempotent — re-running after a partial setup picks up where it
//     stopped (the size-check + swapon).
//
// The swap file persists across reboots only if /etc/fstab is updated;
// we DON'T touch fstab — this is a build-time helper, not a permanent
// host change. Operators on production should provision their own swap.
func ensureBuildSwap() error {
	if active, _ := os.ReadFile("/proc/swaps"); len(strings.Split(strings.TrimSpace(string(active)), "\n")) > 1 {
		return fmt.Errorf("swap already active: build OOM was due to RSS not absent swap")
	}
	const swapPath = "/var/cache/jabali/build-swap"
	const sizeBytes = int64(2 * 1024 * 1024 * 1024) // 2 GiB
	if err := os.MkdirAll(filepath.Dir(swapPath), 0o755); err != nil {
		return fmt.Errorf("mkdir swap parent: %w", err)
	}
	st, statErr := os.Stat(swapPath)
	if statErr != nil || st.Size() < sizeBytes {
		if err := exec.Command("fallocate", "-l", "2G", swapPath).Run(); err != nil {
			return fmt.Errorf("fallocate %s: %w", swapPath, err)
		}
	}
	if err := os.Chmod(swapPath, 0o600); err != nil {
		return fmt.Errorf("chmod swap: %w", err)
	}
	// mkswap is idempotent on an existing valid swap signature but
	// rejects an already-active file — that's why we exit early at
	// the /proc/swaps gate above.
	if out, err := exec.Command("mkswap", swapPath).CombinedOutput(); err != nil {
		return fmt.Errorf("mkswap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("swapon", swapPath).CombinedOutput(); err != nil {
		return fmt.Errorf("swapon: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
