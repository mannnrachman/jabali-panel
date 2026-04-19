package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// wordpressDeleteReq is the input shape for wordpress.delete.
type wordpressDeleteReq struct {
	OSUser  string `json:"os_user"`  // domain owner (e.g. "shuki")
	Docroot string `json:"docroot"`  // /home/shuki/domains/example.com/public_html
}

// wordpressDeleteResp is the output shape for wordpress.delete.
type wordpressDeleteResp struct {
	Status string `json:"status"` // "deleted"
}

func wordpressDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req wordpressDeleteReq
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate required fields
	if req.OSUser == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "os_user is required",
		}
	}
	if req.Docroot == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "docroot is required",
		}
	}

	// Validate docroot is within /home/<osuser>/domains/
	if err := validateDocrootPath(req.OSUser, req.Docroot); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid docroot: %v", err),
		}
	}

	// Targets to remove from the docroot. wp-*.php is glob-expanded
	// Go-side because systemd-run invokes rm with argv (no shell) — a
	// literal "wp-*.php" silently no-ops, leaving wp-config.php (with
	// the DB password) on disk after a "delete". Standard wp directories
	// + non-wp WordPress files are listed explicitly.
	wpDirs := []string{
		"wp-admin",
		"wp-content",
		"wp-includes",
		"readme.html",
		"license.txt",
		"index.php",
		// xmlrpc.php ships with WP but doesn't start with "wp-" so the
		// wp-*.php glob below skips it. Listed explicitly.
		"xmlrpc.php",
	}

	targets := make([]string, 0, len(wpDirs)+16)
	for _, name := range wpDirs {
		targets = append(targets, filepath.Join(req.Docroot, name))
	}
	if matches, err := filepath.Glob(filepath.Join(req.Docroot, "wp-*.php")); err == nil {
		// Defense-in-depth: every match must be inside the validated
		// docroot. Glob doesn't follow symlinks but a malicious entry
		// could still be a path outside the tree on a buggy FS.
		for _, m := range matches {
			if filepath.Dir(m) == req.Docroot {
				targets = append(targets, m)
			}
		}
	}

	// Build and run the delete command under systemd-run as the OS user.
	// Best-effort: a missing file is not an error (rm -f), and one failed
	// delete shouldn't block the rest.
	for _, fullPath := range targets {
		cmd := buildSystemdRunCmd(ctx,
			req.OSUser,
			"rm", "-rf", fullPath,
		)
		_ = cmd.Run()
	}

	return wordpressDeleteResp{
		Status: "deleted",
	}, nil
}

func init() {
	Default.Register("wordpress.delete", wordpressDeleteHandler)
}
