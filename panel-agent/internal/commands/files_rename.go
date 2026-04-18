package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/filesafe"
)

// filesRenameParams is the input shape for files.rename.
type filesRenameParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	OldPath  string `json:"old_path"`
	NewPath  string `json:"new_path"`
}

// filesRenameResponse is the output shape for files.rename.
type filesRenameResponse struct {
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
	Renamed bool   `json:"renamed"`
}

func filesRenameHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filesRenameParams
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
	if p.OldPath == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "old_path required",
		}
	}
	if p.NewPath == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "new_path required",
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

	// Validate and resolve both paths
	oldPath, err := scope.Resolve(p.OldPath)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("old_path validation failed: %v", err),
		}
	}

	newPath, err := scope.Resolve(p.NewPath)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("new_path validation failed: %v", err),
		}
	}

	// Rename file
	if err := os.Rename(oldPath, newPath); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to rename: %v", err),
		}
	}

	return &filesRenameResponse{
		OldPath: oldPath,
		NewPath: newPath,
		Renamed: true,
	}, nil
}

func init() {
	Default.Register("files.rename", filesRenameHandler)
}
