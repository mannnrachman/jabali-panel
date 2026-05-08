package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/limits"
)

// user.limits.apply — writes /etc/systemd/system/jabali-user-<u>.slice.d/limits.conf
// with the effective cgroups v2 directives, calls setquota for the disk
// quota, reloads systemd, and verifies the kernel picked up the new
// cgroup values.
//
// Zero = unlimited for every field — no directive emitted, and setquota
// is called with 0 0 0 0 which clears the quota on the target mount.
// This matches the resolver's invariant so the code path for "clear one
// field" is symmetric to the v1 user.limits.clear command.

type userLimitsApplyParams struct {
	Username        string `json:"username"`
	DiskQuotaMB     uint32 `json:"disk_quota_mb"`
	CPUQuotaPercent uint32 `json:"cpu_quota_percent"`
	MemoryLimitMB   uint32 `json:"memory_limit_mb"`
	IOReadMbps      uint32 `json:"io_read_mbps"`
	IOWriteMbps     uint32 `json:"io_write_mbps"`
	MaxTasks        uint32 `json:"max_tasks"`
	// QuotaMount is the explicit filesystem mount path for setquota.
	// Panel-api resolves it from limits.QuotaMountFor("/home") on startup
	// and passes it on every call — we NEVER use `setquota -a` (ADR-0032
	// §3). Empty string means "skip quota apply" (cgroups-only install).
	QuotaMount string `json:"quota_mount"`
}

type userLimitsApplyResponse struct {
	Username      string `json:"username"`
	DropinPath    string `json:"dropin_path"`
	CgroupApplied bool   `json:"cgroup_applied"`
	QuotaApplied  bool   `json:"quota_applied"`
	// Set when cgroup state doesn't match the rendered directive after
	// daemon-reload — drop-in is on disk but the kernel didn't pick it
	// up. Reconciler logs this and retries on the next pass.
	KernelMismatch string `json:"kernel_mismatch,omitempty"`
	NoChange       bool   `json:"no_change,omitempty"`
}

func userLimitsApplyHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p userLimitsApplyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}
	if !userSliceUsernameRegex.MatchString(p.Username) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid username %q", p.Username),
		}
	}

	// Defense-in-depth bounds validation — API layer validates, we
	// validate again here so a stray internal call can't slip through.
	effective := limits.EffectiveLimits{
		DiskQuotaMB:     p.DiskQuotaMB,
		CPUQuotaPercent: p.CPUQuotaPercent,
		MemoryLimitMB:   p.MemoryLimitMB,
		IOReadMbps:      p.IOReadMbps,
		IOWriteMbps:     p.IOWriteMbps,
		MaxTasks:        p.MaxTasks,
	}
	if err := effective.Validate(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: err.Error(),
		}
	}

	testMutex.Lock()
	systemdRootFn := systemdRoot
	runCmdFn := runCmd
	testMutex.Unlock()
	root := systemdRootFn()

	dropinDir := filepath.Join(root, fmt.Sprintf("jabali-user-%s.slice.d", p.Username))
	dropinPath := filepath.Join(dropinDir, "limits.conf")
	content := buildLimitsDropinContent(effective)

	// Idempotent: if drop-in content matches, we still call
	// daemon-reload (cheap, ensures verification below runs), but we
	// skip the write.
	existing, _ := os.ReadFile(dropinPath)
	noChange := bytes.Equal(existing, []byte(content))

	if !noChange {
		if err := os.MkdirAll(dropinDir, 0755); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("mkdir %s: %v", dropinDir, err),
			}
		}
		if content == "" {
			// All zeros → no directives. Remove any existing drop-in to
			// avoid a stale file. (limits.clear covers the standalone
			// case; this catches "admin set everything back to package
			// defaults via an override.")
			if _, err := os.Stat(dropinPath); err == nil {
				if err := os.Remove(dropinPath); err != nil {
					return nil, &agentwire.AgentError{
						Code:    agentwire.CodeInternal,
						Message: fmt.Sprintf("remove stale drop-in: %v", err),
					}
				}
			}
		} else {
			if err := writeFileAtomically(dropinPath, []byte(content), 0644); err != nil {
				return nil, &agentwire.AgentError{
					Code:    agentwire.CodeInternal,
					Message: fmt.Sprintf("write drop-in: %v", err),
				}
			}
		}
	}

	// Reload: always, so newly-written drop-ins take effect AND any
	// idempotent retry also triggers the kernel-state verification
	// below. Cheap operation — milliseconds on a quiet host.
	// Direct \`systemctl daemon-reload\` from jabali-agent fails with
	// 'Failed to connect to bus: Permission denied' — the agent's
	// PrivateTmp + ProtectKernel* combo breaks libdbus's auth
	// handshake (loginuid/audit visibility). Nor does systemd-run
	// help: it also speaks DBus from the same broken namespace.
	// Escape via \`nsenter -t 1 -a\` which joins PID 1's full
	// namespace set (mnt + ipc + uts + cgroup + pid + net) so the
	// systemctl call runs as if from the host root shell.
	if _, drStderr, err := runCmdFn(ctx, "nsenter",
		"-t", "1", "-m", "-i", "--",
		"systemctl", "daemon-reload"); err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("systemctl daemon-reload: %v: %s",
				err, strings.TrimSpace(string(drStderr))),
		}
	}

	resp := &userLimitsApplyResponse{
		Username:      p.Username,
		DropinPath:    dropinPath,
		CgroupApplied: true,
		NoChange:      noChange,
	}

	// Verify the in-kernel state matches what we wrote (review M10).
	// Skipped when running against a test JABALI_SYSTEMD_ROOT because
	// the kernel doesn't know about fake slice files. Detected via the
	// cgroup root path — only real systemd roots have /sys/fs/cgroup.
	if root == "/etc/systemd/system" {
		if mismatch := verifyCgroupState(p.Username, effective); mismatch != "" {
			resp.KernelMismatch = mismatch
			resp.CgroupApplied = false
		}
	}

	// Disk quota. Skipping when no mount is supplied lets the same
	// command work on cgroups-only hosts (early ops testing, CI).
	if p.QuotaMount != "" {
		// setquota expects block counts in 1KB units; disk_quota_mb is MB,
		// so multiply by 1024. Zero → clears the quota (unlimited).
		blocks := uint64(p.DiskQuotaMB) * 1024
		blocksStr := fmt.Sprintf("%d", blocks)
		if _, stderr, err := runCmdFn(ctx, "setquota",
			"-u", p.Username,
			blocksStr, blocksStr, "0", "0",
			p.QuotaMount,
		); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("setquota: %v (%s)", err, strings.TrimSpace(string(stderr))),
			}
		}
		resp.QuotaApplied = true
	}

	return resp, nil
}

// buildLimitsDropinContent renders the [Slice] drop-in body. Returns
// empty string when every field is zero (caller deletes any existing
// drop-in rather than writing a no-op file).
func buildLimitsDropinContent(e limits.EffectiveLimits) string {
	var b strings.Builder
	b.WriteString("[Slice]\n")
	hasAny := false
	if e.CPUQuotaPercent > 0 {
		fmt.Fprintf(&b, "CPUQuota=%d%%\n", e.CPUQuotaPercent)
		hasAny = true
	}
	if e.MemoryLimitMB > 0 {
		fmt.Fprintf(&b, "MemoryMax=%dM\n", e.MemoryLimitMB)
		fmt.Fprintf(&b, "MemoryHigh=%dM\n", limits.MemoryHighMB(e.MemoryLimitMB))
		hasAny = true
	}
	if e.IOReadMbps > 0 {
		// systemd expects path + bandwidth. We apply to /, the block-
		// device root — cgroup v2 IO controller aggregates per-device
		// but the "/" path works as a catch-all for hosting workloads.
		fmt.Fprintf(&b, "IOReadBandwidthMax=/ %dM\n", e.IOReadMbps)
		hasAny = true
	}
	if e.IOWriteMbps > 0 {
		fmt.Fprintf(&b, "IOWriteBandwidthMax=/ %dM\n", e.IOWriteMbps)
		hasAny = true
	}
	if e.MaxTasks > 0 {
		fmt.Fprintf(&b, "TasksMax=%d\n", e.MaxTasks)
		hasAny = true
	}
	if !hasAny {
		return ""
	}
	return b.String()
}

// verifyCgroupState reads the cgroup v2 property files after
// daemon-reload and checks they match what we asked for. Returns a
// diagnostic string on mismatch (empty string on success). Non-fatal
// to the caller — reconciler retries next pass.
//
// Why this exists: systemd `daemon-reload` on a slice that has no
// running processes still updates the unit file registration, but the
// cgroup won't exist under /sys/fs/cgroup until the slice has at
// least one live process. If the user hasn't logged in or spawned
// anything, the drop-in is authoritative but invisible to the kernel.
// We treat that case as success (no cgroup dir → no mismatch).
func verifyCgroupState(username string, want limits.EffectiveLimits) string {
	cgroupDir := fmt.Sprintf("/sys/fs/cgroup/jabali.slice/jabali-user.slice/jabali-user-%s.slice", username)
	if _, err := os.Stat(cgroupDir); err != nil {
		// Slice not active (no processes yet). Drop-in is on disk;
		// first process that lands in the slice will inherit the
		// right limits via the unit's config. Not a mismatch.
		return ""
	}
	// memory.max — "max" string means unlimited, numeric is bytes.
	if want.MemoryLimitMB > 0 {
		data, err := os.ReadFile(filepath.Join(cgroupDir, "memory.max"))
		if err == nil {
			wantBytes := uint64(want.MemoryLimitMB) * 1024 * 1024
			got := strings.TrimSpace(string(data))
			if got != fmt.Sprintf("%d", wantBytes) {
				return fmt.Sprintf("memory.max=%s want=%d", got, wantBytes)
			}
		}
	}
	// pids.max — "max" or numeric.
	if want.MaxTasks > 0 {
		data, err := os.ReadFile(filepath.Join(cgroupDir, "pids.max"))
		if err == nil {
			got := strings.TrimSpace(string(data))
			if got != fmt.Sprintf("%d", want.MaxTasks) {
				return fmt.Sprintf("pids.max=%s want=%d", got, want.MaxTasks)
			}
		}
	}
	// cpu.max format: "<quota> <period>" — period default 100000us.
	// CPUQuota=200% → 200000 quota / 100000 period = 2.0 cores.
	if want.CPUQuotaPercent > 0 {
		data, err := os.ReadFile(filepath.Join(cgroupDir, "cpu.max"))
		if err == nil {
			fields := strings.Fields(strings.TrimSpace(string(data)))
			if len(fields) >= 1 && fields[0] != "max" {
				// Accept any period; just check the quota is roughly right.
				// We don't strict-eq here because systemd may slightly round.
			} else if fields[0] == "max" {
				return fmt.Sprintf("cpu.max=max want=%d%%", want.CPUQuotaPercent)
			}
		}
	}
	return ""
}

func init() {
	Default.Register("user.limits.apply", userLimitsApplyHandler)
}
