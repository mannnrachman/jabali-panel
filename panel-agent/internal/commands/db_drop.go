package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// dbDropParams is the input shape for db.drop.
type dbDropParams struct {
	DBName string `json:"db_name"`
}

// dbDropResponse is the output shape for db.drop.
type dbDropResponse struct {
	OK bool `json:"ok"`
}

// dbNameRegex is shared with db.create.
var dbDropNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func dbDropHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dbDropParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate db_name format.
	if !dbDropNameRegex.MatchString(p.DBName) {
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

	// Escape the database name using backticks.
	escapedDBName, err := EscapeMariaDBIdentifier(p.DBName)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid database name",
		}
	}

	// Build the DROP DATABASE IF EXISTS command.
	sql := fmt.Sprintf("DROP DATABASE IF EXISTS %s", escapedDBName)

	cmd := exec.CommandContext(ctx, "mysql", "-e", sql)
	if err := cmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "failed to drop database",
		}
	}

	return dbDropResponse{OK: true}, nil
}

func init() {
	Default.Register("db.drop", dbDropHandler)
}
