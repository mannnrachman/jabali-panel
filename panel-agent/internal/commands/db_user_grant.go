package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// dbUserGrantParams is the input shape for db_user.grant.
type dbUserGrantParams struct {
	DBName     string `json:"db_name"`
	DBUserName string `json:"db_user_name"`
	GrantLevel string `json:"grant_level"` // "rw" or "ro"
}

// dbUserGrantResponse is the output shape for db_user.grant.
type dbUserGrantResponse struct {
	OK bool `json:"ok"`
}

var dbUserGrantNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func dbUserGrantHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dbUserGrantParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate db_name format.
	if !dbUserGrantNameRegex.MatchString(p.DBName) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid database name",
		}
	}

	// Validate db_user_name format.
	if !dbUserGrantNameRegex.MatchString(p.DBUserName) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid database user name",
		}
	}

	// Validate grant_level.
	if p.GrantLevel != "rw" && p.GrantLevel != "ro" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid grant_level; must be 'rw' or 'ro'",
		}
	}

	// Escape database name using backticks.
	escapedDBName, err := EscapeMariaDBIdentifier(p.DBName)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid database name",
		}
	}

	// Escape username literal for the 'name'@'localhost' form.
	escapedUsername, err := EscapeMariaDBLiteral(p.DBUserName)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid username",
		}
	}

	// Build the GRANT command based on grant_level.
	var grantSql string
	if p.GrantLevel == "rw" {
		grantSql = fmt.Sprintf(
			"GRANT ALL PRIVILEGES ON %s.* TO %s@'localhost'",
			escapedDBName,
			escapedUsername,
		)
	} else {
		// "ro"
		grantSql = fmt.Sprintf(
			"GRANT SELECT ON %s.* TO %s@'localhost'",
			escapedDBName,
			escapedUsername,
		)
	}

	// Issue the GRANT and FLUSH PRIVILEGES in one command.
	sql := grantSql + "; FLUSH PRIVILEGES"

	cmd := exec.CommandContext(ctx, "mysql", "-e", sql)
	if err := cmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "failed to grant privileges",
		}
	}

	return dbUserGrantResponse{OK: true}, nil
}

func init() {
	Default.Register("db_user.grant", dbUserGrantHandler)
}
