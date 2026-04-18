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
	DBName     string   `json:"db_name"`
	DBUserName string   `json:"db_user_name"`
	GrantLevel string   `json:"grant_level"` // "rw" or "ro" (legacy, fallback)
	Privileges []string `json:"privileges"` // ["SELECT", "INSERT", ...] or ["ALL"]
}

// dbUserRevokeResponse is the output shape for db_user.revoke.
type dbUserRevokeResponse struct {
	OK bool `json:"ok"`
}

var dbUserRevokeNameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)

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

	// Determine which privilege list to use: privileges (new) or fallback to grant_level (legacy).
	var privStr string
	if len(p.Privileges) > 0 {
		// Use privileges array.
		normalized, err := validateAndNormalizePrivileges(p.Privileges)
		if err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("invalid privileges: %v", err),
			}
		}
		privStr = normalized
	} else {
		// Fallback to grant_level for backward compatibility.
		if p.GrantLevel == "rw" {
			privStr = "ALL"
		} else if p.GrantLevel == "ro" {
			privStr = "SELECT"
		} else {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: "either privileges or valid grant_level must be provided",
			}
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

	// Build the REVOKE command.
	var revokeSql string
	if privStr == "ALL" {
		revokeSql = fmt.Sprintf(
			"REVOKE ALL PRIVILEGES ON %s.* FROM %s@'localhost'",
			escapedDBName,
			escapedUsername,
		)
	} else {
		revokeSql = fmt.Sprintf(
			"REVOKE %s ON %s.* FROM %s@'localhost'",
			privStr,
			escapedDBName,
			escapedUsername,
		)
	}

	// Issue the REVOKE and FLUSH PRIVILEGES in one command.
	sql := revokeSql + "; FLUSH PRIVILEGES"

	cmd := exec.CommandContext(ctx, "mysql", "-e", sql)
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
