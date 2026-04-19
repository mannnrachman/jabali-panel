package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/filesafe"
)

// files.ingest — take a file in /tmp (written by panel-api from an
// incoming chunked upload) and move it into the user's scope at the
// requested destination path.
//
// The `tmp_path` MUST start with "/tmp/jabali-upload-" so a malicious
// panel-api request can't coerce this command into relocating, e.g.,
// /etc/shadow into the user's homedir. panel-api always writes into
// that prefix so this gate is a belt-and-braces check against it
// being tricked via request tampering.

type filesIngestParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	TmpPath  string `json:"tmp_path"`
	DestPath string `json:"dest_path"`
}

type filesIngestResponse struct {
	DestPath string `json:"dest_path"`
	Bytes    int64  `json:"bytes"`
}

const tmpUploadPrefix = "/tmp/jabali-upload-"

func filesIngestHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filesIngestParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}
	if p.Username == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "username required"}
	}
	if p.TmpPath == "" || p.DestPath == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "tmp_path and dest_path required"}
	}
	if !strings.HasPrefix(p.TmpPath, tmpUploadPrefix) || strings.Contains(p.TmpPath[len(tmpUploadPrefix):], "/") {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "tmp_path must be a jabali-upload scratch file",
		}
	}

	homeDir := fmt.Sprintf("/home/%s", p.Username)
	scope, err := filesafe.NewScope(p.UserID, p.Username, []string{homeDir})
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("scope: %v", err),
		}
	}
	dst, err := scope.Resolve(p.DestPath)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("dest_path validation failed: %v", err),
		}
	}
	if _, err := os.Lstat(dst); err == nil {
		_ = os.Remove(p.TmpPath)
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "target path already exists",
		}
	}

	// Try rename first — same filesystem, atomic. /tmp and user homedirs
	// are typically on the same partition; if not, fall back to copy+unlink.
	if err := os.Rename(p.TmpPath, dst); err == nil {
		info, _ := os.Stat(dst)
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		if err := chownIngestFile(dst, p.Username); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("chown: %v", err),
			}
		}
		_ = os.Chmod(dst, 0o644)
		return &filesIngestResponse{DestPath: dst, Bytes: size}, nil
	}

	// Cross-filesystem fallback: copy the bytes, then unlink the source.
	in, err := os.Open(p.TmpPath)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("open tmp: %v", err)}
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("open dst: %v", err)}
	}
	size, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("copy: %v", err)}
	}
	_ = os.Remove(p.TmpPath)
	if err := chownIngestFile(dst, p.Username); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chown: %v", err)}
	}
	return &filesIngestResponse{DestPath: dst, Bytes: size}, nil
}

// chownIngestFile sets the ingested file's owner/group to <user>:www-data,
// matching the M9.5 ownership contract for files inside a user's homedir.
func chownIngestFile(path, username string) error {
	return chownToHostingUser(path, username)
}

func init() {
	Default.Register("files.ingest", filesIngestHandler)
}
