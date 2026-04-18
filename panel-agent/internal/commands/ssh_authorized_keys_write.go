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

// sshAuthorizedKeysWriteParams is the input shape for ssh.authorized_keys.write.
type sshAuthorizedKeysWriteParams struct {
	Username string   `json:"username"`
	Keys     []string `json:"keys"`
}

// sshAuthorizedKeysWriteResponse is the output shape for ssh.authorized_keys.write.
type sshAuthorizedKeysWriteResponse struct {
	Username string `json:"username"`
	KeyCount int    `json:"key_count"`
	Written  bool   `json:"written"`
}

func sshAuthorizedKeysWriteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p sshAuthorizedKeysWriteParams
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
	tmpPath := filepath.Join(sshDir, "authorized_keys.tmp")

	// Ensure $HOME/.ssh/ exists with correct ownership and mode (0700)
	if err := ensureSSHDir(sshDir, u); err != nil {
		return nil, err
	}

	// Prepare the keys content: join with newlines, ensure trailing newline
	content := strings.Join(p.Keys, "\n")
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	// Write to temporary file
	if err := os.WriteFile(tmpPath, []byte(content), 0600); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to write temp file: %v", err),
		}
	}

	// Chown the temporary file
	uid, gid := parseUID(u)
	if err := os.Chown(tmpPath, uid, gid); err != nil {
		_ = os.Remove(tmpPath) // Best effort cleanup
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to chown temp file: %v", err),
		}
	}

	// Atomic rename
	if err := os.Rename(tmpPath, authorizedKeysPath); err != nil {
		_ = os.Remove(tmpPath) // Best effort cleanup
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to rename authorized_keys: %v", err),
		}
	}

	return &sshAuthorizedKeysWriteResponse{
		Username: p.Username,
		KeyCount: len(p.Keys),
		Written:  true,
	}, nil
}

// ensureSSHDir ensures .ssh directory exists with owner and mode 0700.
func ensureSSHDir(sshDir string, u *user.User) *agentwire.AgentError {
	// Check if directory exists
	info, err := os.Stat(sshDir)
	if err == nil {
		// Directory exists; verify/fix ownership if needed
		uid, gid := parseUID(u)
		if err := os.Chown(sshDir, uid, gid); err != nil {
			return &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to chown .ssh dir: %v", err),
			}
		}
		// Verify mode is 0700; if not, fix it
		if info.Mode()&0777 != 0700 {
			if err := os.Chmod(sshDir, 0700); err != nil {
				return &agentwire.AgentError{
					Code:    agentwire.CodeInternal,
					Message: fmt.Sprintf("failed to chmod .ssh dir: %v", err),
				}
			}
		}
		return nil
	}

	// Directory doesn't exist; create it
	if !os.IsNotExist(err) {
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to stat .ssh dir: %v", err),
		}
	}

	uid, gid := parseUID(u)
	if err := os.Mkdir(sshDir, 0700); err != nil {
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to create .ssh dir: %v", err),
		}
	}

	if err := os.Chown(sshDir, uid, gid); err != nil {
		_ = os.RemoveAll(sshDir) // Best effort cleanup
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to chown .ssh dir: %v", err),
		}
	}

	return nil
}

// parseUID converts user.User to numeric uid/gid.
func parseUID(u *user.User) (int, int) {
	var uid, gid int
	// Parse uid
	if _, err := fmt.Sscanf(u.Uid, "%d", &uid); err != nil {
		uid = -1
	}
	// Parse gid
	if _, err := fmt.Sscanf(u.Gid, "%d", &gid); err != nil {
		gid = -1
	}
	return uid, gid
}

func init() {
	Default.Register("ssh.authorized_keys.write", sshAuthorizedKeysWriteHandler)
}
