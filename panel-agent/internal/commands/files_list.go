package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/filesafe"
)

// filesListParams is the input shape for files.list.
type filesListParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Path     string `json:"path"`
}

// filesListEntry represents a single file/directory entry.
type filesListEntry struct {
	Name      string `json:"name"`
	IsDir     bool   `json:"is_dir"`
	Size      int64  `json:"size"`
	Mode      string `json:"mode"`
	ModTime   string `json:"mod_time"`
	IsSymlink bool   `json:"is_symlink"`
}

// filesListResponse is the output shape for files.list.
type filesListResponse struct {
	Path    string           `json:"path"`
	Entries []filesListEntry `json:"entries"`
}

func filesListHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filesListParams
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

	// Open directory
	entries, err := os.ReadDir(resolvedPath)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to list directory: %v", err),
		}
	}

	// Convert entries (use Lstat to avoid symlink traversal)
	result := []filesListEntry{}
	for _, entry := range entries {
		// Use Lstat to avoid following symlinks
		entryPath := filepath.Join(resolvedPath, entry.Name())
		info, err := os.Lstat(entryPath)
		if err != nil {
			// Skip entries we can't stat
			continue
		}
		result = append(result, filesListEntry{
			Name:      entry.Name(),
			IsDir:     info.IsDir(),
			Size:      info.Size(),
			Mode:      info.Mode().String(),
			ModTime:   info.ModTime().String(),
			IsSymlink: (info.Mode() & os.ModeSymlink) != 0,
		})
	}

	return &filesListResponse{
		Path:    resolvedPath,
		Entries: result,
	}, nil
}

func init() {
	Default.Register("files.list", filesListHandler)
}
