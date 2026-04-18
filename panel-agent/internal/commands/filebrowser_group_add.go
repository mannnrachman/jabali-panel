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

// filebrowserGroupAddParams is the input shape for filebrowser.group.add.
type filebrowserGroupAddParams struct {
	Username string `json:"username"` // The per-user group to add filebrowser to
}

// filebrowserGroupAddResponse is the output shape for filebrowser.group.add.
type filebrowserGroupAddResponse struct {
	GroupName string `json:"group_name"`
	Added     bool   `json:"added,omitempty"`
	AlreadyMember bool `json:"already_member,omitempty"`
}

func filebrowserGroupAddHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filebrowserGroupAddParams
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

	// Check if filebrowser is already a member of the group
	idCmd := exec.CommandContext(ctx, "id", "filebrowser")
	var idOut, idErr bytes.Buffer
	idCmd.Stdout = &idOut
	idCmd.Stderr = &idErr

	if err := idCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to check filebrowser user: %v", err),
		}
	}

	// Check if p.Username appears in the groups output
	idOutput := idOut.String()
	if strings.Contains(idOutput, fmt.Sprintf("(%s)", p.Username)) {
		// Already a member
		return &filebrowserGroupAddResponse{
			GroupName:     p.Username,
			AlreadyMember: true,
		}, nil
	}

	// Add filebrowser to the per-user group via usermod -aG <username> filebrowser
	usermodCmd := exec.CommandContext(ctx, "usermod", "-aG", p.Username, "filebrowser")
	var usermodOut, usermodErr bytes.Buffer
	usermodCmd.Stdout = &usermodOut
	usermodCmd.Stderr = &usermodErr

	if err := usermodCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to add filebrowser to group %s: %v (stderr: %s)", p.Username, err, usermodErr.String()),
		}
	}

	return &filebrowserGroupAddResponse{
		GroupName: p.Username,
		Added:     true,
	}, nil
}

func init() {
	Default.Register("filebrowser.group.add", filebrowserGroupAddHandler)
}
