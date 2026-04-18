package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/filesafe"
)

// filesWriteParams is the input shape for files.write.
type filesWriteParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Path     string `json:"path"`
	Content  string `json:"content"`
	Mode     string `json:"mode,omitempty"` // "append" or "overwrite" (default)
}

// filesWriteResponse is the output shape for files.write.
type filesWriteResponse struct {
	Path      string `json:"path"`
	BytesWritten int64  `json:"bytes_written"`
}

func filesWriteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filesWriteParams
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

	// Determine file open flags
	var flags int
	if p.Mode == "append" {
		flags = os.O_WRONLY | os.O_APPEND | os.O_CREATE
	} else {
		flags = os.O_WRONLY | os.O_TRUNC | os.O_CREATE
	}

	// Open file safely
	file, err := scope.Open(resolvedPath, flags, 0644)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to open file: %v", err),
		}
	}
	defer file.Close()

	// Write content
	n, err := file.WriteString(p.Content)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to write file: %v", err),
		}
	}

	return &filesWriteResponse{
		Path:         resolvedPath,
		BytesWritten: int64(n),
	}, nil
}

func init() {
	Default.Register("files.write", filesWriteHandler)
}
