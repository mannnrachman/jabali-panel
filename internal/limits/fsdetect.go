package limits

import (
	"fmt"
	"os/exec"
	"strings"
)

// FilesystemType classifies the filesystem under a given directory
// for the purpose of deciding whether POSIX user quota works.
type FilesystemType string

const (
	FSExt4   FilesystemType = "ext4"
	FSExt3   FilesystemType = "ext3"
	FSExt2   FilesystemType = "ext2"
	FSXFS    FilesystemType = "xfs"
	FSBtrfs  FilesystemType = "btrfs"
	FSZFS    FilesystemType = "zfs"
	FSTmpfs  FilesystemType = "tmpfs"
	FSOther  FilesystemType = "other"
	FSUnknown FilesystemType = "unknown"
)

// SupportsPOSIXQuota reports whether this FS type can back `setquota -u`.
// ext2/3/4 work out of the box with `usrquota` mount. xfs needs
// `xfs_quota -x -c 'enable -u'` after remount — this function returns
// true for xfs (the install script adds the extra step).
//
// btrfs + zfs: their quota models are subvolume/dataset-scoped, don't
// map to per-user hosting quotas. Explicitly unsupported in v1.
func (t FilesystemType) SupportsPOSIXQuota() bool {
	switch t {
	case FSExt2, FSExt3, FSExt4, FSXFS:
		return true
	default:
		return false
	}
}

// detectFS is overridable in tests so they don't have to shell out.
var detectFS = func(path string) (string, error) {
	// `stat -fc %T` returns the filesystem type name as the kernel sees it
	// (e.g. "ext2/ext3", "xfs", "btrfs", "zfs", "tmpfs").
	// Using stat(1) not syscall.Statfs so we match exactly what the
	// install.sh script probes — avoids two sources of truth.
	out, err := exec.Command("stat", "-fc", "%T", path).Output()
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// DetectFilesystem returns the FilesystemType of the mount containing
// path. Returns FSUnknown + nil error when stat succeeds but the type
// name isn't one we recognise — callers should treat unknown as
// unsupported and fail loud.
func DetectFilesystem(path string) (FilesystemType, error) {
	raw, err := detectFS(path)
	if err != nil {
		return FSUnknown, err
	}
	// stat(1) returns composite names like "ext2/ext3" on kernels that
	// can't distinguish; treat those as the latest-compatible variant.
	switch raw {
	case "ext4":
		return FSExt4, nil
	case "ext2/ext3", "ext3":
		return FSExt3, nil
	case "ext2":
		return FSExt2, nil
	case "xfs":
		return FSXFS, nil
	case "btrfs":
		return FSBtrfs, nil
	case "zfs":
		return FSZFS, nil
	case "tmpfs":
		return FSTmpfs, nil
	default:
		// Capture the raw name so the caller's error path can surface
		// it in a runbook entry.
		return FilesystemType(strings.ToLower(raw)), nil
	}
}
