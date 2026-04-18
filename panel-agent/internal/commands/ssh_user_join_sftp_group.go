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

// sshUserJoinSFTPGroupParams is the input shape for ssh.user.join_sftp_group.
type sshUserJoinSFTPGroupParams struct {
	Username string `json:"username"`
}

// sshUserJoinSFTPGroupResponse is the output shape for ssh.user.join_sftp_group.
type sshUserJoinSFTPGroupResponse struct {
	Username string `json:"username"`
	Joined   bool   `json:"joined,omitempty"`
	AlreadyMember bool `json:"already_member,omitempty"`
}

const sftpGroupName = "jabali-sftp"

func sshUserJoinSFTPGroupHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p sshUserJoinSFTPGroupParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Check if user is already a member of jabali-sftp group
	isMember, err := isUserInGroup(ctx, p.Username, sftpGroupName)
	if err != nil {
		return nil, err
	}

	if isMember {
		// Already a member; idempotent success
		return &sshUserJoinSFTPGroupResponse{
			Username:      p.Username,
			AlreadyMember: true,
		}, nil
	}

	// Add user to the group via usermod -aG jabali-sftp <username>
	usermodCmd := exec.CommandContext(ctx, "usermod", "-aG", sftpGroupName, p.Username)
	var usermodOut, usermodErr bytes.Buffer
	usermodCmd.Stdout = &usermodOut
	usermodCmd.Stderr = &usermodErr

	if err := usermodCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to add %q to group %q: %v (stderr: %s)", p.Username, sftpGroupName, err, usermodErr.String()),
		}
	}

	return &sshUserJoinSFTPGroupResponse{
		Username: p.Username,
		Joined:   true,
	}, nil
}

// isUserInGroup checks if a user is a member of a group.
// Uses `id -nG <username>` to get the list of group names.
func isUserInGroup(ctx context.Context, username, groupName string) (bool, *agentwire.AgentError) {
	idCmd := exec.CommandContext(ctx, "id", "-nG", username)
	var idOut, idErr bytes.Buffer
	idCmd.Stdout = &idOut
	idCmd.Stderr = &idErr

	if err := idCmd.Run(); err != nil {
		return false, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to check groups for user %q: %v", username, err),
		}
	}

	// id -nG returns space-separated group names
	groups := strings.Fields(idOut.String())
	for _, g := range groups {
		if g == groupName {
			return true, nil
		}
	}

	return false, nil
}

func init() {
	Default.Register("ssh.user.join_sftp_group", sshUserJoinSFTPGroupHandler)
}
