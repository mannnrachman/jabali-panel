package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// filebrowserUserListResponse is the output shape for filebrowser.user.list.
type filebrowserUserListResponse struct {
	Usernames []string `json:"usernames"`
}

func filebrowserUserListHandler(ctx context.Context, params json.RawMessage) (any, error) {
	// This command takes no parameters
	listCmd := exec.CommandContext(ctx, "filebrowser",
		"--database", filebrowserDB,
		"users", "ls")

	var out, errBuf bytes.Buffer
	listCmd.Stdout = &out
	listCmd.Stderr = &errBuf

	if err := listCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to list filebrowser users: %v (stderr: %s)", err, errBuf.String()),
		}
	}

	// Parse output: each line is a username (filebrowser outputs one per line)
	var usernames []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			usernames = append(usernames, line)
		}
	}

	return &filebrowserUserListResponse{
		Usernames: usernames,
	}, nil
}

func init() {
	Default.Register("filebrowser.user.list", filebrowserUserListHandler)
}
