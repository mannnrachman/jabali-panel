// fs.stat — privileged path-only stat. Used by reconciler probes
// (WordPress version.php drift detection) and admin health endpoints
// where the user-scoped `files.stat` would refuse the path because
// it lives outside /home/<user>/.
//
// Returns existence + size + mode + mtime + symlink/dir flags. Caller
// must NOT pass user-supplied paths — this command intentionally
// skips the home-dir prefix check that `files.stat` enforces.
//
// On non-existent files: returns exists=false with the rest zeroed
// (NOT an agent error). Distinguishes "file gone" from "syscall
// failed" cleanly.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type fsStatParams struct {
	Path string `json:"path"`
}

type fsStatResponse struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	Size      int64  `json:"size"`
	Mode      string `json:"mode,omitempty"`
	IsDir     bool   `json:"is_dir,omitempty"`
	IsSymlink bool   `json:"is_symlink,omitempty"`
	ModTime   string `json:"mod_time,omitempty"`
}

func fsStatHandler(_ context.Context, params json.RawMessage) (any, error) {
	var p fsStatParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	if p.Path == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "path required"}
	}

	// Lstat so symlinks are reported as such (Stat follows links).
	info, err := os.Lstat(p.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fsStatResponse{Path: p.Path, Exists: false}, nil
		}
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	return fsStatResponse{
		Path:      p.Path,
		Exists:    true,
		Size:      info.Size(),
		Mode:      "0" + strconv.FormatUint(uint64(info.Mode().Perm()), 8),
		IsDir:     info.IsDir(),
		IsSymlink: info.Mode()&os.ModeSymlink != 0,
		ModTime:   info.ModTime().UTC().Format(time.RFC3339),
	}, nil
}

func init() {
	Default.Register("fs.stat", fsStatHandler)
}
