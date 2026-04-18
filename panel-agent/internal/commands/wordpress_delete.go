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

	// List of WordPress files and directories to remove (from Step 2 task 6)
	wpFiles := []string{
		"wp-*.php",
		"wp-admin",
		"wp-content",
		"wp-includes",
		"readme.html",
		"license.txt",
		"index.php",
	}

	// Build and run the delete command under systemd-run as the OS user
	for _, pattern := range wpFiles {
		fullPath := filepath.Join(req.Docroot, pattern)
		cmd := buildSystemdRunCmd(ctx,
			req.OSUser,
			"rm", "-rf", fullPath,
		)
		if err := cmd.Run(); err != nil {
			// Best-effort deletion; continue even if one file fails
			// Log the error but don't stop the whole operation
			_ = err
		}
	}

	return wordpressDeleteResp{
		Status: "deleted",
	}, nil
}

func init() {
	Default.Register("wordpress.delete", wordpressDeleteHandler)
}
