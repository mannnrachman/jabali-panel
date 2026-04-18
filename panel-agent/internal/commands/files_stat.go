package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/filesafe"
)

// filesStatParams is the input shape for files.stat.
type filesStatParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Path     string `json:"path"`
}

// filesStatResponse is the output shape for files.stat.
type filesStatResponse struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Mode      string `json:"mode"`
	IsDir     bool   `json:"is_dir"`
	ModTime   string `json:"mod_time"`
	IsSymlink bool   `json:"is_symlink"`
}

func filesStatHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filesStatParams
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

	// Lstat file (don't follow symlinks)
	info, err := os.Lstat(resolvedPath)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to stat file: %v", err),
		}
	}

	return &filesStatResponse{
		Path:      resolvedPath,
		Size:      info.Size(),
		Mode:      info.Mode().String(),
		IsDir:     info.IsDir(),
		ModTime:   info.ModTime().String(),
		IsSymlink: (info.Mode() & os.ModeSymlink) != 0,
	}, nil
}

func init() {
	Default.Register("files.stat", filesStatHandler)
}
