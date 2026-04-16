package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// userPasswordParams is the input shape for user.password.
type userPasswordParams struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// userPasswordResponse is the output shape for user.password.
type userPasswordResponse struct {
	Username string `json:"username"`
}

func userPasswordHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p userPasswordParams
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

	// Reject if password is empty.
	if p.Password == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "password cannot be empty",
		}
	}

	// Check if user is protected.
	if protectedUsers[p.Username] {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodePermissionDenied,
			Message: fmt.Sprintf("cannot change password for protected user %q", p.Username),
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

	// Set password via chpasswd.
	chpasswdCmd := exec.CommandContext(ctx, "chpasswd")
	chpasswdCmd.Stdin = strings.NewReader(p.Username + ":" + p.Password + "\n")
	if err := chpasswdCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("chpasswd failed: %v", err),
		}
	}

	return userPasswordResponse{
		Username: p.Username,
	}, nil
}

func init() {
	Default.Register("user.password", userPasswordHandler)
}
