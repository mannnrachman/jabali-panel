package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/filesafe"
)

// filesDeleteParams is the input shape for files.delete.
type filesDeleteParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Path     string `json:"path"`
	Recursive bool  `json:"recursive,omitempty"`
}

// filesDeleteResponse is the output shape for files.delete.
type filesDeleteResponse struct {
	Path    string `json:"path"`
	Deleted bool   `json:"deleted"`
}

func filesDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filesDeleteParams
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

	// Prevent deletion of docroot roots themselves
	for _, docroot := range scope.OwnedDocroots {
		if resolvedPath == docroot {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: "cannot delete docroot directory",
			}
		}
	}

	// Delete file or directory
	var deleteErr error
	if p.Recursive {
		deleteErr = os.RemoveAll(resolvedPath)
	} else {
		deleteErr = os.Remove(resolvedPath)
	}

	if deleteErr != nil {
		// If path doesn't exist, return success (idempotent)
		if !os.IsNotExist(deleteErr) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to delete: %v", deleteErr),
			}
		}
	}

	return &filesDeleteResponse{
		Path:    resolvedPath,
		Deleted: true,
	}, nil
}

func init() {
	Default.Register("files.delete", filesDeleteHandler)
}
