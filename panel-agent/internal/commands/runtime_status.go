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

type runtimeStatusParams struct {
	Username string `json:"username"`
	Domain   string `json:"domain"`
}

type runtimeStatusResponse struct {
	Status   string `json:"status"` // active, inactive, failed, not-found
	SubState string `json:"sub_state,omitempty"`
	PID      int    `json:"pid,omitempty"`
}

func runtimeStatusHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p runtimeStatusParams
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

	// Check status using systemctl show
	cmd := exec.CommandContext(ctx, "sudo", "-u", p.Username)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("XDG_RUNTIME_DIR=%s", runtimeDir),
	)
	cmd.Args = append(cmd.Args, "systemctl", "--user", "show", serviceName, "--property=ActiveState", "--property=SubState", "--property=MainPID", "--property=LoadState")

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("systemctl status check failed: %v", err),
		}
	}

	lines := strings.Split(stdout.String(), "\n")
	props := make(map[string]string)
	for _, line := range lines {
		if parts := strings.SplitN(line, "=", 2); len(parts) == 2 {
			props[parts[0]] = parts[1]
		}
	}

	loadState := props["LoadState"]
	activeState := props["ActiveState"]
	subState := props["SubState"]
	pidStr := props["MainPID"]

	if loadState == "not-found" || loadState == "" {
		return &runtimeStatusResponse{
			Status: "not-found",
		}, nil
	}

	pid, _ := strconv.Atoi(pidStr)

	return &runtimeStatusResponse{
		Status:   activeState,
		SubState: subState,
		PID:      pid,
	}, nil
}

func init() {
	Default.Register("runtime.status", runtimeStatusHandler)
}
