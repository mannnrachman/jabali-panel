package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/filesafe"
)

// files.chmod — change permission bits on a file or directory within
// the user's scope. Ownership is NOT changed: everything in the user's
// homedir is already owned by <user>:www-data (that's the M9.5 contract),
// and exposing chown would mean exposing root-equivalent capability here.
//
// Mode is a Unix-style octal string ("0644", "755", "0o755" all accepted).
// Only the low 12 bits are honoured — setuid/setgid/sticky allowed so the
// user can tidy up legacy uploads, but higher bits are masked out.

type filesChmodParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Path     string `json:"path"`
	Mode     string `json:"mode"`
}

type filesChmodResponse struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
}

func filesChmodHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filesChmodParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}
	if p.Username == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "username required"}
	}
	if p.Path == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "path required"}
	}
	if p.Mode == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "mode required"}
	}

	mode, err := parseChmodMode(p.Mode)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid mode: %v", err),
		}
	}

	homeDir := fmt.Sprintf("/home/%s", p.Username)
	scope, err := filesafe.NewScope(p.UserID, p.Username, []string{homeDir})
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to create scope: %v", err),
		}
	}
	path, err := scope.Resolve(p.Path)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("path validation failed: %v", err),
		}
	}

	// Lstat first — if it's a symlink we do NOT chase; we leave symlinks
	// as-is rather than chmod-ing the target, which would be surprising.
	info, err := os.Lstat(path)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("stat: %v", err),
		}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "cannot chmod a symlink",
		}
	}

	if err := os.Chmod(path, mode); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("chmod: %v", err),
		}
	}

	return &filesChmodResponse{
		Path: path,
		Mode: fmt.Sprintf("%04o", mode),
	}, nil
}

// parseChmodMode accepts "0644", "644", "0o644" and returns an os.FileMode
// masked to the low 12 bits (rwx for u/g/o plus setuid/setgid/sticky).
// Any higher-bit characters (file-type bits like 0100000) are rejected
// rather than silently stripped — if a caller sends `100644` they're
// probably confused, and we'd rather surface that than silently do the
// wrong thing. The 12-bit cap is the Linux chmod(2) contract.
func parseChmodMode(s string) (os.FileMode, error) {
	// Accept "0o" prefix for the TOML/Go-style writer; otherwise bare octal.
	trimmed := s
	if len(trimmed) > 2 && trimmed[0] == '0' && (trimmed[1] == 'o' || trimmed[1] == 'O') {
		trimmed = trimmed[2:]
	}
	n, err := strconv.ParseUint(trimmed, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("not an octal number: %q", s)
	}
	if n > 0o7777 {
		return 0, fmt.Errorf("mode out of range (max 0o7777): %q", s)
	}
	return os.FileMode(n), nil
}

func init() {
	Default.Register("files.chmod", filesChmodHandler)
}
