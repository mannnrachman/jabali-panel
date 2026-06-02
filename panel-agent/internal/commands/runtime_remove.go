package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type runtimeRemoveParams struct {
	Username string `json:"username"`
	Domain   string `json:"domain"`
}

type runtimeRemoveResponse struct {
	Success bool `json:"success"`
}

func runtimeRemoveHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p runtimeRemoveParams
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
	userSystemdDir := filepath.Join(u.HomeDir, ".config", "systemd", "user")
	servicePath := filepath.Join(userSystemdDir, serviceName)
	envFilePath := filepath.Join(userSystemdDir, fmt.Sprintf("jabali-rt-%s.env", p.Domain))

	// Stop and disable service (best effort, don't fail if already stopped or disabled)
	_ = systemctlUserExec(ctx, p.Username, runtimeDir, "stop", serviceName)
	_ = systemctlUserExec(ctx, p.Username, runtimeDir, "disable", serviceName)

	// If it's a docker runtime, make sure we clean up the container too
	dockerExe := resolveExecutable(p.Username, "docker")
	if dockerExe != "" {
		dockerRmCmd := exec.CommandContext(ctx, "sudo", "-u", p.Username, dockerExe, "rm", "-f", fmt.Sprintf("jabali-rt-%s", p.Domain))
		_ = dockerRmCmd.Run()
	}

	// Remove service file if exists
	if _, err := os.Stat(servicePath); err == nil {
		if err := os.Remove(servicePath); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to delete service file: %v", err),
			}
		}

		// Daemon reload
		_ = systemctlUserExec(ctx, p.Username, runtimeDir, "daemon-reload")
	}

	// Remove the EnvironmentFile too (best effort).
	_ = os.Remove(envFilePath)

	return &runtimeRemoveResponse{Success: true}, nil
}

func init() {
	Default.Register("runtime.remove", runtimeRemoveHandler)
}
