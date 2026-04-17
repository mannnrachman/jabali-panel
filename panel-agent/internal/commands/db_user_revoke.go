package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// dbUserRevokeParams is the input shape for db_user.revoke.
type dbUserRevokeParams struct {
	DBName     string `json:"db_name"`
	DBUserName string `json:"db_user_name"`
	GrantLevel string `json:"grant_level"` // "rw" or "ro"
}

// dbUserRevokeResponse is the output shape for db_user.revoke.
type dbUserRevokeResponse struct {
	OK bool `json:"ok"`
}

var dbUserRevokeNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func dbUserRevokeHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dbUserRevokeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate db_name format.
	if !dbUserRevokeNameRegex.MatchString(p.DBName) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid database name",
		}
	}

	// Validate db_user_name format.
	if !dbUserRevokeNameRegex.MatchString(p.DBUserName) {
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

	// Build the REVOKE command based on grant_level.
	var revokeSql string
	if p.GrantLevel == "rw" {
		revokeSql = fmt.Sprintf(
			"REVOKE ALL PRIVILEGES ON %s.* FROM %s@'localhost'",
			escapedDBName,
			escapedUsername,
		)
	} else {
		// "ro"
		revokeSql = fmt.Sprintf(
			"REVOKE SELECT ON %s.* FROM %s@'localhost'",
			escapedDBName,
			escapedUsername,
		)
	}

	// Issue the REVOKE and FLUSH PRIVILEGES in one command.
	sql := revokeSql + "; FLUSH PRIVILEGES"

	cmd := exec.CommandContext(ctx, "mysql", "--defaults-file=/root/.my.cnf", "-e", sql)
	if err := cmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "failed to revoke privileges",
		}
	}

	return dbUserRevokeResponse{OK: true}, nil
}

func init() {
	Default.Register("db_user.revoke", dbUserRevokeHandler)
}
