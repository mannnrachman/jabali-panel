package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/filesafe"
)

// classifyFSWriteErr turns a low-level write/chown/rename error into an
// agent error with a stable machine-readable code in the message prefix.
// The panel-api router matches on these prefixes to return 507 (quota)
// or 507 (disk full) instead of opaque 500s.
//
//   - EDQUOT  → "quota_exceeded: …"
//   - ENOSPC  → "disk_full: …"
//   - EACCES  → permission_denied code (most specific agentwire code)
//   - default → CodeInternal with the raw syserror text
func classifyFSWriteErr(stage string, err error) *agentwire.AgentError {
	switch {
	case errors.Is(err, syscall.EDQUOT):
		return &agentwire.AgentError{
			Code:    agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("quota_exceeded: %s: %v", stage, err),
		}
	case errors.Is(err, syscall.ENOSPC):
		return &agentwire.AgentError{
			Code:    agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("disk_full: %s: %v", stage, err),
		}
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return &agentwire.AgentError{
			Code:    agentwire.CodePermissionDenied,
			Message: fmt.Sprintf("%s: %v", stage, err),
		}
	default:
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("%s: %v", stage, err),
		}
	}
}

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

	// Enforce content size cap at 100MB
	const maxContentSize int64 = 100 * 1024 * 1024
	if int64(len(p.Content)) > maxContentSize {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("content exceeds 100MB limit (%d bytes)", len(p.Content)),
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

	// For overwrite mode (or if file doesn't exist), use temp-file-then-rename pattern
	if p.Mode != "append" {
		// Lookup user to get uid/gid
		u, err := user.Lookup(p.Username)
		if err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("failed to lookup user %q: %v", p.Username, err),
			}
		}
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)

		// Generate temp filename with random suffix
		randBytes := make([]byte, 8)
		if _, err := rand.Read(randBytes); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to generate random suffix: %v", err),
			}
		}
		tmpName := fmt.Sprintf("%s.tmp.%s", resolvedPath, hex.EncodeToString(randBytes))

		// Create temp file with 0600 perms (read/write for owner only)
		tmpFile, err := os.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, classifyFSWriteErr("create_tempfile", err)
		}

		// Write content
		n, err := tmpFile.WriteString(p.Content)
		if err != nil {
			tmpFile.Close()
			os.Remove(tmpName)
			return nil, classifyFSWriteErr("write_tempfile", err)
		}

		// Fsync to ensure data is written
		if err := tmpFile.Sync(); err != nil {
			tmpFile.Close()
			os.Remove(tmpName)
			return nil, classifyFSWriteErr("sync_tempfile", err)
		}

		tmpFile.Close()

		// Chown temp file to user:www-data. This is where EDQUOT
		// typically surfaces on upload: agent wrote the bytes as root
		// (unlimited quota), and the kernel re-charges the bytes to the
		// target uid when ownership transfers. If the recipient is over
		// quota, chown returns EDQUOT even though the write succeeded.
		if err := os.Chown(tmpName, uid, gid); err != nil {
			os.Remove(tmpName)
			return nil, classifyFSWriteErr("chown_tempfile", err)
		}

		// Chmod to 0640: owner rw, www-data group r (nginx static read),
		// other none. Matches per-user FPM isolation; blocks cross-user shell reads.
		if err := os.Chmod(tmpName, 0640); err != nil {
			os.Remove(tmpName)
			return nil, classifyFSWriteErr("chmod_tempfile", err)
		}

		// Atomic rename
		if err := os.Rename(tmpName, resolvedPath); err != nil {
			os.Remove(tmpName)
			return nil, classifyFSWriteErr("rename_tempfile", err)
		}

		return &filesWriteResponse{
			Path:         resolvedPath,
			BytesWritten: int64(n),
		}, nil
	}

	// Append mode: open existing file or create new one
	file, err := scope.Open(resolvedPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0640)
	if err != nil {
		return nil, classifyFSWriteErr("open_append", err)
	}
	defer file.Close()

	// Write content
	n, err := file.WriteString(p.Content)
	if err != nil {
		return nil, classifyFSWriteErr("write_append", err)
	}

	return &filesWriteResponse{
		Path:         resolvedPath,
		BytesWritten: int64(n),
	}, nil
}

func init() {
	Default.Register("files.write", filesWriteHandler)
}
