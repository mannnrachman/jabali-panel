package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// cronTailLogParams is the input for cron.tail_log command.
type cronTailLogParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	JobID    string `json:"job_id"`
	Lines    *int   `json:"lines,omitempty"` // Defaults to 50 if not provided
}

// cronTailLogResponse is the output from cron.tail_log.
type cronTailLogResponse struct {
	Log string `json:"log"`
}

func cronTailLogHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p cronTailLogParams
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

	// Default lines to 50 if not provided
	lines := 50
	if p.Lines != nil {
		lines = *p.Lines
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

	// Query journalctl for the service logs
	cmd := exec.CommandContext(ctx, "sudo", "-u", p.Username)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("XDG_RUNTIME_DIR=%s", runtimeDir),
	)
	cmd.Args = append(cmd.Args,
		"journalctl", "--user",
		"-u", fmt.Sprintf("jabali-cron-%s.service", p.JobID),
		"-n", fmt.Sprintf("%d", lines),
		"-o", "cat",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// If journalctl fails (e.g., no logs yet), return empty log or error
		// Most common: "No entries found" which is not an error condition
		return &cronTailLogResponse{
			Log: stdout.String(),
		}, nil
	}

	return &cronTailLogResponse{
		Log: strings.TrimSpace(stdout.String()),
	}, nil
}

func init() {
	Default.Register("cron.tail_log", cronTailLogHandler)
}
