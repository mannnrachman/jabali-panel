package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// user.limits.clear — removes the M18 limits drop-in and clears the
// POSIX user quota. Called on user delete and on override-revert
// workflows. Idempotent: safe to call when the drop-in doesn't exist
// and when no quota is currently set for the user.

type userLimitsClearParams struct {
	Username   string `json:"username"`
	QuotaMount string `json:"quota_mount"` // empty skips the setquota step
}

type userLimitsClearResponse struct {
	Username     string `json:"username"`
	DropinRemoved bool  `json:"dropin_removed"`
	QuotaCleared bool   `json:"quota_cleared"`
}

func userLimitsClearHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p userLimitsClearParams
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

	testMutex.Lock()
	systemdRootFn := systemdRoot
	runCmdFn := runCmd
	testMutex.Unlock()
	root := systemdRootFn()

	dropinPath := filepath.Join(root, fmt.Sprintf("jabali-user-%s.slice.d", p.Username), "limits.conf")
	resp := &userLimitsClearResponse{Username: p.Username}

	if err := os.Remove(dropinPath); err == nil {
		resp.DropinRemoved = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("remove drop-in: %v", err),
		}
	}

	// Reload either way — if the drop-in existed, systemd needs to
	// forget it; if not, it's a cheap no-op.
	// systemd-run wrap: see user_limits_apply for the namespace
	// rationale.
	if _, drStderr, err := runCmdFn(ctx, "systemd-run",
		"--pipe", "--wait", "--quiet", "--collect",
		"--service-type=oneshot", "--",
		"systemctl", "daemon-reload"); err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("daemon-reload: %v: %s",
				err, strings.TrimSpace(string(drStderr))),
		}
	}

	if p.QuotaMount != "" {
		// Clear the quota: 0 0 0 0 means no soft, no hard, no inode
		// limits — user becomes unbounded on this mount.
		if _, _, err := runCmdFn(ctx, "setquota",
			"-u", p.Username,
			"0", "0", "0", "0",
			p.QuotaMount,
		); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("setquota clear: %v", err),
			}
		}
		resp.QuotaCleared = true
	}

	return resp, nil
}

func init() {
	Default.Register("user.limits.clear", userLimitsClearHandler)
}
