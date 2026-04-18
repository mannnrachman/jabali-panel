package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// dbSizeParams is the input shape for db.size.
type dbSizeParams struct {
	DBName string `json:"db_name"`
}

// dbSizeResponse is the output shape for db.size.
type dbSizeResponse struct {
	SizeBytes int64 `json:"size_bytes"`
}

// dbNameRegex validates MariaDB database name format.
// Must start with letter, contain only letters, digits, underscores, hyphens.
// Max 64 chars (MariaDB limit for unquoted identifiers).
var dbSizeNameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

func dbSizeHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dbSizeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate db_name format.
	if !dbSizeNameRegex.MatchString(p.DBName) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid database name",
		}
	}

	// Escape the database name as a literal for use in the WHERE clause.
	escapedDBName, err := EscapeMariaDBLiteral(p.DBName)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid database name",
		}
	}

	// Query information_schema.tables to sum data_length and index_length.
	// Use the escaped literal in the WHERE clause.
	sql := fmt.Sprintf(
		"SELECT COALESCE(SUM(data_length+index_length),0) FROM information_schema.tables WHERE table_schema = %s",
		escapedDBName,
	)

	cmd := exec.CommandContext(ctx, "mysql", "-Nse", sql)
	output, err := cmd.Output()
	if err != nil {
		// Even if the query fails or the database doesn't exist, we return size=0
		// rather than failing. This ensures the list endpoint doesn't 500.
		return dbSizeResponse{SizeBytes: 0}, nil
	}

	sizeStr := strings.TrimSpace(string(output))
	if sizeStr == "" {
		return dbSizeResponse{SizeBytes: 0}, nil
	}

	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		// Malformed output: return 0 rather than fail.
		return dbSizeResponse{SizeBytes: 0}, nil
	}

	return dbSizeResponse{SizeBytes: size}, nil
}

func init() {
	Default.Register("db.size", dbSizeHandler)
}
