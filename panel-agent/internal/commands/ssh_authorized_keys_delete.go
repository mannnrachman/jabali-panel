package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// sshAuthorizedKeysDeleteParams is the input shape for ssh.authorized_keys.delete.
type sshAuthorizedKeysDeleteParams struct {
	Username string `json:"username"`
}

// sshAuthorizedKeysDeleteResponse is the output shape for ssh.authorized_keys.delete.
type sshAuthorizedKeysDeleteResponse struct {
	Username string `json:"username"`
	Deleted  bool   `json:"deleted"`
}

func sshAuthorizedKeysDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p sshAuthorizedKeysDeleteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate username and resolve user's homedir
	u, err := user.Lookup(p.Username)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("user %q not found: %v", p.Username, err),
		}
	}

	homeDir := u.HomeDir
	sshDir := filepath.Join(homeDir, ".ssh")
	authorizedKeysPath := filepath.Join(sshDir, "authorized_keys")

	// Attempt to remove the authorized_keys file
	if err := os.Remove(authorizedKeysPath); err != nil {
		// Ignore "file not found" errors; that's idempotent
		if !os.IsNotExist(err) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to delete authorized_keys: %v", err),
			}
		}
	}

	return &sshAuthorizedKeysDeleteResponse{
		Username: p.Username,
		Deleted:  true,
	}, nil
}

func init() {
	Default.Register("ssh.authorized_keys.delete", sshAuthorizedKeysDeleteHandler)
}
