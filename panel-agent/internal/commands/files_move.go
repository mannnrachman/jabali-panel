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

// files.move — relocate a file or directory to a new path within the
// user's scope. Distinct from files.rename (which is same-parent only):
// move allows different parent directories so the UI can implement
// drag-and-drop of rows into folder rows.
//
// Safety: both source and destination are resolved through the same
// filesafe scope as every other files.* handler, so the user cannot
// move a file out of their homedir or name a destination outside of it.

type filesMoveParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	OldPath  string `json:"old_path"`
	NewPath  string `json:"new_path"`
}

type filesMoveResponse struct {
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
	Moved   bool   `json:"moved"`
}

func filesMoveHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filesMoveParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	if p.Username == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "username required"}
	}
	if p.OldPath == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "old_path required"}
	}
	if p.NewPath == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "new_path required"}
	}

	homeDir := fmt.Sprintf("/home/%s", p.Username)
	scope, err := filesafe.NewScope(p.UserID, p.Username, []string{homeDir})
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to create scope: %v", err),
		}
	}

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

	// Refuse no-op moves — the caller almost certainly dropped a row
	// back onto itself or onto its own parent. Quiet success is confusing.
	if oldPath == newPath {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "source and destination are the same",
		}
	}

	// Refuse moving a directory into itself (mv foo foo/bar). os.Rename
	// would leave the filesystem in a surprising state, or error with
	// EINVAL on some kernels; catching it here produces a clearer error.
	if isDescendant(oldPath, newPath) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "cannot move into a subdirectory of itself",
		}
	}

	// Prevent silent overwrite — if the user drags onto a folder that
	// already contains a same-named child, surface it instead of clobbering.
	if _, err := os.Lstat(newPath); err == nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "target path already exists",
		}
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to move: %v", err),
		}
	}

	return &filesMoveResponse{
		OldPath: oldPath,
		NewPath: newPath,
		Moved:   true,
	}, nil
}

// isDescendant reports whether descendant is the same path as ancestor
// or lives inside it. Uses filepath.Rel so "/a/b" vs "/a/bar" doesn't
// trigger a false positive (string-prefix check would).
func isDescendant(ancestor, descendant string) bool {
	if ancestor == descendant {
		return true
	}
	rel, err := filepath.Rel(ancestor, descendant)
	if err != nil {
		return false
	}
	// Rel returns "../..." when descendant is NOT under ancestor.
	if rel == "." || rel == "" {
		return true
	}
	if len(rel) >= 2 && rel[0] == '.' && rel[1] == '.' {
		return false
	}
	return true
}

func init() {
	Default.Register("files.move", filesMoveHandler)
}
