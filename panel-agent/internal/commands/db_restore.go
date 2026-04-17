package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// dbRestoreParams is the input shape for db.restore.
type dbRestoreParams struct {
	DBName string `json:"db_name"`
	Path   string `json:"path"`
}

// dbRestoreResponse is the output shape for db.restore.
type dbRestoreResponse struct {
	OK bool `json:"ok"`
}

// dbRestoreNameRegex validates MariaDB database name format.
var dbRestoreNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func dbRestoreHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dbRestoreParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate db_name format.
	if !dbRestoreNameRegex.MatchString(p.DBName) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid database name",
		}
	}

	// Reject dangerous patterns (second layer of defense).
	if strings.Contains(p.DBName, "/") ||
		strings.Contains(p.DBName, "\\") ||
		strings.Contains(p.DBName, ";") ||
		strings.Contains(p.DBName, "\n") ||
		strings.Contains(p.DBName, "\r") ||
		strings.Contains(p.DBName, " ") ||
		strings.Contains(p.DBName, ".") {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid database name",
		}
	}

	// Validate path is under /var/lib/jabali/restore/ (critical security check).
	if !strings.HasPrefix(p.Path, "/var/lib/jabali/restore/") {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "restore path must be under /var/lib/jabali/restore/",
		}
	}

	// Ensure the file exists and is readable.
	f, err := os.Open(p.Path)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "restore file not found or not readable",
		}
	}
	defer f.Close()

	// Run mysql with stdin from the file, database name as positional argument.
	cmd := exec.CommandContext(ctx, "mysql", p.DBName)
	cmd.Stdin = f

	if err := cmd.Run(); err != nil {
		// Always delete the file, whether restore succeeds or fails.
		_ = os.Remove(p.Path)
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "failed to restore database",
		}
	}

	// Delete the file on success.
	if err := os.Remove(p.Path); err != nil {
		// Log but don't fail if cleanup fails; restore succeeded.
		// In production, admin might need to manually clean up.
	}

	return dbRestoreResponse{OK: true}, nil
}

func init() {
	Default.Register("db.restore", dbRestoreHandler)
}
