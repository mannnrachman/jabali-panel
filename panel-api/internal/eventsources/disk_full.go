package eventsources

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

const (
	diskFullTick     = 10 * time.Minute
	diskFullCoolOff  = 30 * time.Minute
	diskWarnPercent  = 85.0
	diskCritPercent  = 95.0
)

// diskFullMounts lists the filesystems we care about. Extra mounts (an
// operator-attached /mnt/backup) can be added here without touching the
// notification pipeline.
var diskFullMounts = []string{"/", "/var/www", "/var/lib/mysql"}

// runDiskFull polls the filesystem usage every 10 minutes and fires a
// warn-or-crit envelope when any mount crosses the thresholds. Both
// tiers are independently deduped: once a mount has fired "warn" it
// won't fire again for 30 minutes — enough breathing room for the
// operator to act, short enough that a transient dip-and-recover path
// doesn't silence real sustained problems.
func runDiskFull(ctx context.Context, d Deps) {
	// Fire a pass immediately on start so an operator booting into a
	// full disk isn't blind for 10 minutes.
	diskFullPass(ctx, d)
	tick := time.NewTicker(diskFullTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		diskFullPass(ctx, d)
	}
}

func diskFullPass(ctx context.Context, d Deps) {
	for _, mount := range diskFullMounts {
		used, total, err := diskUsage(mount)
		if err != nil {
			// Missing mount (dev box without /var/lib/mysql, for
			// instance) is not an error — skip quietly.
			continue
		}
		if total == 0 {
			continue
		}
		pct := float64(used) / float64(total) * 100.0
		switch {
		case pct >= diskCritPercent:
			fireDiskEvent(ctx, d, mount, pct, "disk.full.crit", models.NotificationSeverityError)
		case pct >= diskWarnPercent:
			fireDiskEvent(ctx, d, mount, pct, "disk.full.warn", models.NotificationSeverityWarning)
		}
	}
}

func fireDiskEvent(ctx context.Context, d Deps, mount string, pct float64, kind, severity string) {
	tag := "mount:" + mount
	if !shouldFire(ctx, d, kind, tag, diskFullCoolOff) {
		return
	}
	_, err := d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: kind,
		Severity:  severity,
		Title:     fmt.Sprintf("%s at %.0f%% full", mount, pct),
		Body:      fmt.Sprintf("Filesystem %s is %.1f%% full. (%s)", mount, pct, tag),
		Deeplink:  "/admin/system",
	})
	if err != nil {
		d.Log.Warn("eventsources: publish disk event failed", "mount", mount, "err", err)
	}
}

// diskUsage wraps syscall.Statfs with 64-bit block math so the caller
// doesn't juggle Bavail/Blocks/Bsize pointers. Returns (used, total)
// in bytes; an error means the mount doesn't exist or we can't stat it.
//
// Note: we use Blocks - Bavail for "used" rather than Blocks - Bfree so
// the number reflects what a non-root process sees — reserved blocks
// aren't usable by the panel anyway.
func diskUsage(mount string) (used, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(mount, &st); err != nil {
		return 0, 0, err
	}
	total = uint64(st.Blocks) * uint64(st.Bsize)
	avail := uint64(st.Bavail) * uint64(st.Bsize)
	used = total - avail
	return used, total, nil
}
