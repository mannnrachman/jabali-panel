package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// filebrowserUserDeleteParams is the input shape for filebrowser.user.delete.
type filebrowserUserDeleteParams struct {
	Username string `json:"username"`
}

// filebrowserUserDeleteResponse is the output shape for filebrowser.user.delete.
type filebrowserUserDeleteResponse struct {
	Username string `json:"username"`
	Deleted  bool   `json:"deleted,omitempty"`
	NotFound bool   `json:"not_found,omitempty"`
}

func filebrowserUserDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filebrowserUserDeleteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate username format
	if !filebrowserUsernameRegex.MatchString(p.Username) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid username %q: must match ^[a-z][a-z0-9_]{0,31}$", p.Username),
		}
	}

	// Check if user exists first
	findCmd := exec.CommandContext(ctx, "filebrowser",
		"--database", filebrowserDB,
		"users", "find", p.Username)
	var findOut, findErr bytes.Buffer
	findCmd.Stdout = &findOut
	findCmd.Stderr = &findErr
	findExists := findCmd.Run() == nil

	if !findExists {
		// User doesn't exist; idempotent success
		return &filebrowserUserDeleteResponse{
			Username: p.Username,
			NotFound: true,
		}, nil
	}

	// Delete the user
	deleteCmd := exec.CommandContext(ctx, "filebrowser",
		"--database", filebrowserDB,
		"users", "rm", p.Username)

	var delOut, delErrBuf bytes.Buffer
	deleteCmd.Stdout = &delOut
	deleteCmd.Stderr = &delErrBuf

	if err := deleteCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to delete filebrowser user: %v (stderr: %s)", err, delErrBuf.String()),
		}
	}

	return &filebrowserUserDeleteResponse{
		Username: p.Username,
		Deleted:  true,
	}, nil
}

func init() {
	Default.Register("filebrowser.user.delete", filebrowserUserDeleteHandler)
}
