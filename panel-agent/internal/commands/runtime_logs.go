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

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type runtimeLogsParams struct {
	Username string `json:"username"`
	Domain   string `json:"domain"`
	Lines    int    `json:"lines,omitempty"` // defaults to 50
}

type runtimeLogsResponse struct {
	Logs string `json:"logs"`
}

func runtimeLogsHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p runtimeLogsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	if p.Username == "" || p.Domain == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "username and domain are required",
		}
	}

	numLines := p.Lines
	if numLines <= 0 {
		numLines = 50
	}

	u, err := user.Lookup(p.Username)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeNotFound,
			Message: fmt.Sprintf("user %s not found: %v", p.Username, err),
		}
	}

	uid, _ := strconv.Atoi(u.Uid)
	runtimeDir := fmt.Sprintf("/run/user/%d", uid)
	serviceName := fmt.Sprintf("jabali-rt-%s.service", p.Domain)

	// Fetch logs using journalctl --user-unit
	cmd := exec.CommandContext(ctx, "sudo", "-u", p.Username)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("XDG_RUNTIME_DIR=%s", runtimeDir),
	)
	cmd.Args = append(cmd.Args, "journalctl", "--user-unit", serviceName, "-n", strconv.Itoa(numLines), "--no-pager")

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// Best effort, if journalctl returns error (e.g. no logs yet), just return empty or error info
	_ = cmd.Run()

	return &runtimeLogsResponse{
		Logs: stdout.String(),
	}, nil
}

func init() {
	Default.Register("runtime.logs", runtimeLogsHandler)
}
