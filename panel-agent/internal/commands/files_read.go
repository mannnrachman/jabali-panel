package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/filesafe"
)

// filesReadParams is the input shape for files.read.
type filesReadParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Path     string `json:"path"`
	Limit    int64  `json:"limit,omitempty"` // 0 = no limit, default 1MB
}

// filesReadResponse is the output shape for files.read.
type filesReadResponse struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Truncated bool  `json:"truncated,omitempty"`
}

func filesReadHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filesReadParams
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

	// Default limit: 1MB
	if p.Limit == 0 {
		p.Limit = 1 << 20 // 1MB
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

	// Open file safely
	file, err := scope.Open(resolvedPath, 0, 0)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to open file: %v", err),
		}
	}
	defer file.Close()

	// Read with limit
	limitedReader := io.LimitReader(file, p.Limit+1)
	content, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to read file: %v", err),
		}
	}

	truncated := int64(len(content)) > p.Limit
	if truncated {
		content = content[:p.Limit]
	}

	return &filesReadResponse{
		Path:      resolvedPath,
		Content:   string(content),
		Truncated: truncated,
	}, nil
}

func init() {
	Default.Register("files.read", filesReadHandler)
}
