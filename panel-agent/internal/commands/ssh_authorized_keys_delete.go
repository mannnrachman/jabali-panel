package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

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

	sshDir := filepath.Join(u.HomeDir, ".ssh")
	authorizedKeysPath := filepath.Join(sshDir, "authorized_keys")

	// "delete" means remove jabali's managed block ONLY — never the
	// whole file. Operator keys outside the markers survive (incident
	// 2026-05-17: the every-tick reconciler delete was wiping operator
	// SSH access on every jabali host).
	existing, rerr := readAuthorizedKeys(authorizedKeysPath)
	if rerr != nil {
		return nil, rerr
	}
	if existing == "" {
		return &sshAuthorizedKeysDeleteResponse{Username: p.Username, Deleted: true}, nil
	}
	remainder := applyManagedBlock(existing, nil)
	if strings.TrimSpace(remainder) == "" {
		// Nothing but the jabali block was present — clean removal.
		if err := os.Remove(authorizedKeysPath); err != nil && !os.IsNotExist(err) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to delete authorized_keys: %v", err),
			}
		}
	} else if werr := writeAuthorizedKeysAtomic(u, sshDir, authorizedKeysPath, remainder); werr != nil {
		return nil, werr
	}

	return &sshAuthorizedKeysDeleteResponse{
		Username: p.Username,
		Deleted:  true,
	}, nil
}

func init() {
	Default.Register("ssh.authorized_keys.delete", sshAuthorizedKeysDeleteHandler)
}
