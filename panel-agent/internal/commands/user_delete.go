package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// userDeleteParams is the input shape for user.delete.
type userDeleteParams struct {
	Username   string `json:"username"`
	RemoveHome bool   `json:"remove_home"`
}

// userDeleteResponse is the output shape for user.delete.
type userDeleteResponse struct {
	Username    string `json:"username"`
	RemovedHome bool   `json:"removed_home"`
}

// protectedUsers is a hardcoded deny list of users that must never be deleted.
var protectedUsers = map[string]bool{
	"root":   true,
	"jabali": true,
	// Add the service user name here if different from "jabali"
}

func userDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p userDeleteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate username format.
	if !usernameRegex.MatchString(p.Username) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid username %q: must match ^[a-z_][a-z0-9_-]{0,31}$", p.Username),
		}
	}

	// Check if user is protected.
	if protectedUsers[p.Username] {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodePermissionDenied,
			Message: fmt.Sprintf("cannot delete protected user %q", p.Username),
		}
	}

	// Check if user exists.
	checkCmd := exec.CommandContext(ctx, "id", p.Username)
	if err := checkCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeNotFound,
			Message: fmt.Sprintf("user %q does not exist", p.Username),
		}
	}

	// Delete user.
	var deleteCmd *exec.Cmd
	if p.RemoveHome {
		deleteCmd = exec.CommandContext(ctx, "userdel", "--remove", p.Username)
	} else {
		deleteCmd = exec.CommandContext(ctx, "userdel", p.Username)
	}

	if err := deleteCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("userdel failed: %v", err),
		}
	}

	return userDeleteResponse{
		Username:    p.Username,
		RemovedHome: p.RemoveHome,
	}, nil
}

func init() {
	Default.Register("user.delete", userDeleteHandler)
}
