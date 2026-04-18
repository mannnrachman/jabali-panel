package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// cronRemoveParams is the input for cron.remove command.
type cronRemoveParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	JobID    string `json:"job_id"`
}

// cronRemoveResponse is the output from cron.remove.
type cronRemoveResponse struct {
	NoChange bool `json:"no_change,omitempty"`
}

func cronRemoveHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p cronRemoveParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate inputs
	if p.Username == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "username required",
		}
	}
	if p.JobID == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "job_id required",
		}
	}

	// Resolve user's UID
	u, err := user.Lookup(p.Username)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeNotFound,
			Message: fmt.Sprintf("user %s not found: %v", p.Username, err),
		}
	}

	uid, _ := strconv.Atoi(u.Uid)
	runtimeDir := fmt.Sprintf("/run/user/%d", uid)

	// Generate unit file paths
	unitsDir := fmt.Sprintf("/etc/jabali-panel/cron-units/%s", p.Username)
	servicePath := filepath.Join(unitsDir, fmt.Sprintf("jabali-cron-%s.service", p.JobID))
	timerPath := filepath.Join(unitsDir, fmt.Sprintf("jabali-cron-%s.timer", p.JobID))

	// Check if already removed
	serviceExists := fileExists(servicePath)
	timerExists := fileExists(timerPath)
	if !serviceExists && !timerExists {
		return &cronRemoveResponse{
			NoChange: true,
		}, nil
	}

	// Disable and stop the timer
	disableErr := systemctlUserExec(ctx, p.Username, runtimeDir, "disable", "--now", fmt.Sprintf("jabali-cron-%s.timer", p.JobID))
	if disableErr != nil {
		// If user's systemd manager is unreachable, return a specific error
		// but still attempt cleanup of files
		if strings.Contains(disableErr.Error(), "user manager not running") ||
			strings.Contains(disableErr.Error(), "User manager is not running") ||
			strings.Contains(disableErr.Error(), "Connection refused") {
			// Clean up files anyway
			_ = os.Remove(servicePath)
			_ = os.Remove(timerPath)
			return nil, &agentwire.AgentError{
				Code:    "user_manager_unreachable",
				Message: fmt.Sprintf("user %s systemd manager unreachable; files cleaned up locally", p.Username),
			}
		}
		// Other errors are fatal
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to disable timer: %v", disableErr),
		}
	}

	// Reload systemd
	if err := systemctlUserExec(ctx, p.Username, runtimeDir, "daemon-reload"); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to reload systemd: %v", err),
		}
	}

	// Remove unit files
	if err := os.Remove(servicePath); err != nil && !os.IsNotExist(err) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to remove service file: %v", err),
		}
	}

	if err := os.Remove(timerPath); err != nil && !os.IsNotExist(err) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to remove timer file: %v", err),
		}
	}

	return &cronRemoveResponse{}, nil
}

// fileExists checks if a file exists without following symlinks.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func init() {
	Default.Register("cron.remove", cronRemoveHandler)
}
