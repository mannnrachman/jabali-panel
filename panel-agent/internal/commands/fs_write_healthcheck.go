package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// fsWriteHealthcheckParams is the input shape for fs.write_healthcheck.
type fsWriteHealthcheckParams struct {
	Path      string `json:"path"`
	UserGroup string `json:"user_group"`
}

// fsWriteHealthcheckResponse is the output shape for fs.write_healthcheck.
type fsWriteHealthcheckResponse struct {
	Path   string `json:"path"`
	Wrote  bool   `json:"wrote"`
	Exists bool   `json:"exists"`
}

// healthcheckPHPContent is the fixed content written to the health-check file.
const healthcheckPHPContent = `<?php header("Content-Type: text/plain"); echo "ok ", PHP_VERSION, "\n";`

func fsWriteHealthcheckHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p fsWriteHealthcheckParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate path is absolute and reasonable.
	if !strings.HasPrefix(p.Path, "/") {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("path must be absolute: %q", p.Path),
		}
	}

	// Validate user_group format (should be "user:group").
	if !strings.Contains(p.UserGroup, ":") {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("user_group must be in format 'user:group', got %q", p.UserGroup),
		}
	}

	// Check if file already exists (idempotent — don't overwrite).
	if _, err := os.Stat(p.Path); err == nil {
		// File exists.
		return fsWriteHealthcheckResponse{
			Path:   p.Path,
			Wrote:  false,
			Exists: true,
		}, nil
	} else if !os.IsNotExist(err) {
		// Stat failed for a reason other than "not found".
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to stat %q: %v", p.Path, err),
		}
	}

	// Write the health-check file.
	if err := os.WriteFile(p.Path, []byte(healthcheckPHPContent), 0644); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to write %q: %v", p.Path, err),
		}
	}

	// Chown to user:group.
	chownCmd := exec.CommandContext(ctx, "chown", p.UserGroup, p.Path)
	if err := chownCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("chown %q to %s failed: %v", p.Path, p.UserGroup, err),
		}
	}

	return fsWriteHealthcheckResponse{
		Path:   p.Path,
		Wrote:  true,
		Exists: false,
	}, nil
}

func init() {
	Default.Register("fs.write_healthcheck", fsWriteHealthcheckHandler)
}
