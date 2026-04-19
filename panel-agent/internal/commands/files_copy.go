package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/filesafe"
)

// files.copy — recursively copy a scoped path to a new location,
// preserving mode bits and following symlinks as-is (stored as
// symlinks in the copy, not chased). Cross-directory by design: used
// by the Copy / Paste flow and the Phase-2 drag-copy behaviour that
// may come later.

type filesCopyParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	SrcPath  string `json:"src_path"`
	DstPath  string `json:"dst_path"`
}

type filesCopyResponse struct {
	SrcPath string `json:"src_path"`
	DstPath string `json:"dst_path"`
	Bytes   int64  `json:"bytes"`
}

func filesCopyHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filesCopyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}
	if p.Username == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "username required"}
	}
	if p.SrcPath == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "src_path required"}
	}
	if p.DstPath == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "dst_path required"}
	}

	homeDir := fmt.Sprintf("/home/%s", p.Username)
	scope, err := filesafe.NewScope(p.UserID, p.Username, []string{homeDir})
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to create scope: %v", err),
		}
	}
	src, err := scope.Resolve(p.SrcPath)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("src_path validation failed: %v", err),
		}
	}
	dst, err := scope.Resolve(p.DstPath)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("dst_path validation failed: %v", err),
		}
	}
	if src == dst {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "source and destination are the same",
		}
	}
	// Refuse copy of a dir into its own descendant — same shape as move.
	if isDescendant(src, dst) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "cannot copy into a subdirectory of itself",
		}
	}
	if _, err := os.Lstat(dst); err == nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "target path already exists",
		}
	}

	bytes, err := copyTree(src, dst)
	if err != nil {
		// Attempt rollback — copy may have left a partial tree behind.
		_ = os.RemoveAll(dst)
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("copy: %v", err),
		}
	}
	return &filesCopyResponse{
		SrcPath: src,
		DstPath: dst,
		Bytes:   bytes,
	}, nil
}

// copyTree walks src and reproduces the whole subtree at dst.
// Returns total bytes of regular-file content copied (metadata not
// counted). Symlinks are preserved as symlinks to their original
// target strings — we don't chase and we don't validate the target
// is inside the scope, since it's just a string in the filesystem.
func copyTree(src, dst string) (int64, error) {
	var totalBytes int64
	err := filepath.Walk(src, func(p string, _ os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		li, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		switch {
		case li.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := os.Symlink(link, target); err != nil {
				return err
			}
		case li.IsDir():
			if err := os.MkdirAll(target, li.Mode().Perm()); err != nil {
				return err
			}
		case li.Mode().IsRegular():
			n, err := copyFile(p, target, li.Mode().Perm())
			if err != nil {
				return err
			}
			totalBytes += n
		default:
			// Skip devices, sockets, fifos — these don't make sense in
			// a user's homedir anyway and we don't want to accidentally
			// duplicate a special file.
		}
		return nil
	})
	return totalBytes, err
}

func copyFile(src, dst string, perm os.FileMode) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}

func init() {
	Default.Register("files.copy", filesCopyHandler)
}
