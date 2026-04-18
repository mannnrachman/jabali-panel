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

	// Step 1: add filebrowser to the per-user Unix group. This was the
	// original M11 access path, but the user's home dir is chowned
	// <user>:www-data (for nginx/PHP-FPM), so group membership alone
	// does NOT grant filebrowser read access. We keep this step for
	// defense-in-depth and so tooling that checks /etc/group doesn't
	// see a skew between expected and actual members.
	usermodCmd := exec.CommandContext(ctx, "usermod", "-aG", p.Username, "filebrowser")
	var usermodErr bytes.Buffer
	usermodCmd.Stderr = &usermodErr
	if err := usermodCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to add filebrowser to group %s: %v (stderr: %s)", p.Username, err, usermodErr.String()),
		}
	}

	// Step 2: grant filebrowser rx on the user's home via POSIX ACL.
	// This is the actual access-granting step — see
	// plans/m11-filebrowser-session-fix.md "Home dir access" for why
	// group-based access doesn't work on the existing <user>:www-data
	// layout.
	//
	// We set both the access ACL (applies to the dir itself and
	// existing children recursively) and the default ACL (applies to
	// anything new created under the tree).
	homeDir := fmt.Sprintf("/home/%s", p.Username)
	for _, args := range [][]string{
		// Recursive: grant r-x to existing files/dirs.
		{"-R", "-m", "g:filebrowser:rX", homeDir},
		// Default ACL: anything created later inherits the grant.
		{"-R", "-d", "-m", "g:filebrowser:rX", homeDir},
	} {
		cmd := exec.CommandContext(ctx, "setfacl", args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("setfacl %v failed on %s: %v (stderr: %s)", args, homeDir, err, stderr.String()),
			}
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
