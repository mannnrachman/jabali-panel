package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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

	// Create directory
	var mkdirErr error
	if p.Mode == "parents" {
		mkdirErr = os.MkdirAll(resolvedPath, 0755)
	} else {
		mkdirErr = os.Mkdir(resolvedPath, 0755)
	}

	if mkdirErr != nil {
		// If already exists, return success (idempotent)
		if !os.IsExist(mkdirErr) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to create directory: %v", mkdirErr),
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
