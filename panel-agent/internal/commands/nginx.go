package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// nginxTestResponse is the output shape for nginx.test.
type nginxTestResponse struct {
	Valid bool `json:"valid"`
}

// nginxReloadResponse is the output shape for nginx.reload.
type nginxReloadResponse struct {
	Reloaded bool `json:"reloaded"`
}

func nginxTestHandler(ctx context.Context, params json.RawMessage) (any, error) {
	// Run nginx -t to test configuration
	testCmd := exec.CommandContext(ctx, "nginx", "-t")
	var combinedOutput bytes.Buffer
	testCmd.Stdout = &combinedOutput
	testCmd.Stderr = &combinedOutput

	if err := testCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("nginx test failed: %s", combinedOutput.String()),
		}
	}

	return nginxTestResponse{Valid: true}, nil
}

func nginxReloadHandler(ctx context.Context, params json.RawMessage) (any, error) {
	// First test nginx configuration
	_, err := nginxTestHandler(ctx, params)
	if err != nil {
		return nil, err
	}

	// Run systemctl reload nginx
	reloadCmd := exec.CommandContext(ctx, "systemctl", "reload", "nginx")
	var combinedOutput bytes.Buffer
	reloadCmd.Stdout = &combinedOutput
	reloadCmd.Stderr = &combinedOutput

	if err := reloadCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("systemctl reload nginx failed: %s", combinedOutput.String()),
		}
	}

	return nginxReloadResponse{Reloaded: true}, nil
}

func init() {
	Default.Register("nginx.test", nginxTestHandler)
	Default.Register("nginx.reload", nginxReloadHandler)
}
