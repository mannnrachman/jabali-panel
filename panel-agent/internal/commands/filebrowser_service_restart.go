package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// filebrowserServiceRestartResponse is the output shape for filebrowser.service.restart.
type filebrowserServiceRestartResponse struct {
	Service string `json:"service"`
	Restarted bool `json:"restarted"`
}

func filebrowserServiceRestartHandler(ctx context.Context, params json.RawMessage) (any, error) {
	// This command takes no parameters
	cmd := exec.CommandContext(ctx, "systemctl", "restart", "jabali-filebrowser.service")
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to restart filebrowser service: %v (stderr: %s)", err, errBuf.String()),
		}
	}

	return &filebrowserServiceRestartResponse{
		Service:   "jabali-filebrowser.service",
		Restarted: true,
	}, nil
}

func init() {
	Default.Register("filebrowser.service.restart", filebrowserServiceRestartHandler)
}
