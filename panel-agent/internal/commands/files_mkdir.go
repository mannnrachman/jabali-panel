package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"strconv"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/filesafe"
)

// filesMkdirParams is the input shape for files.mkdir.
type filesMkdirParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Path     string `json:"path"`
	Mode     string `json:"mode,omitempty"` // "parents" or empty (default no parents)
}

// filesMkdirResponse is the output shape for files.mkdir.
type filesMkdirResponse struct {
	Path    string `json:"path"`
	Created bool   `json:"created"`
}

func filesMkdirHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filesMkdirParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate inputs
	if p.Username == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "username required",
		}
	}
	if p.Path == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "path required",
		}
	}

	// Create filesafe scope with user's home directory
	homeDir := fmt.Sprintf("/home/%s", p.Username)
	scope, err := filesafe.NewScope(p.UserID, p.Username, []string{homeDir})
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to create scope: %v", err),
		}
	}

	// Validate and resolve path
	resolvedPath, err := scope.Resolve(p.Path)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("path validation failed: %v", err),
		}
	}

	// Create directory with temp permissions (0700), then set final perms + chown
	var mkdirErr error
	if p.Mode == "parents" {
		mkdirErr = os.MkdirAll(resolvedPath, 0700)
	} else {
		mkdirErr = os.Mkdir(resolvedPath, 0700)
	}

	if mkdirErr != nil {
		// If already exists, return success (idempotent)
		if !os.IsExist(mkdirErr) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to create directory: %v", mkdirErr),
			}
		}
		// Directory already exists; adjust ownership if needed
	} else {
		// New directory created; set ownership and permissions
		u, err := user.Lookup(p.Username)
		if err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("failed to lookup user %q: %v", p.Username, err),
			}
		}
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)

		// Chown to user:www-data
		wwwDataGroup, err := user.LookupGroup("www-data")
		wwwDataGid := gid
		if err == nil {
			wwwDataGid, _ = strconv.Atoi(wwwDataGroup.Gid)
		}

		if err := os.Chown(resolvedPath, uid, wwwDataGid); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to chown directory: %v", err),
			}
		}

		// Chmod to 0750: owner rwx, www-data group r-x (nginx reads static + traverses),
		// other none. Matches deployed docroot perms; blocks cross-user shell reads.
		if err := os.Chmod(resolvedPath, 0750); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to chmod directory: %v", err),
			}
		}
	}

	return &filesMkdirResponse{
		Path:    resolvedPath,
		Created: mkdirErr == nil,
	}, nil
}

func init() {
	Default.Register("files.mkdir", filesMkdirHandler)
}
