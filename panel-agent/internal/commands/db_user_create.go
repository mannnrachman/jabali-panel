package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// dbUserCreateParams is the input shape for db_user.create.
type dbUserCreateParams struct {
	DBUserName string `json:"db_user_name"`
	Password   string `json:"password"`
}

// dbUserCreateResponse is the output shape for db_user.create.
type dbUserCreateResponse struct {
	OK bool `json:"ok"`
}

// dbUserNameRegex validates MariaDB database user name format.
// Must start with letter, contain only letters, digits, underscores.
// Max 63 chars to leave room for @localhost suffix.
var dbUserNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func dbUserCreateHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dbUserCreateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate db_user_name format.
	if !dbUserNameRegex.MatchString(p.DBUserName) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid database user name",
		}
	}

	if p.Password == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "password cannot be empty",
		}
	}

	// Escape the username literal for the 'name'@'localhost' form.
	escapedUsername, err := EscapeMariaDBLiteral(p.DBUserName)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid username",
		}
	}

	// Escape the password literal.
	escapedPassword, err := EscapeMariaDBLiteral(p.Password)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid password",
		}
	}

	// Build the CREATE USER command.
	// Form: CREATE USER '<name>'@'localhost' IDENTIFIED BY '<password>';
	sql := fmt.Sprintf(
		"CREATE USER %s@'localhost' IDENTIFIED BY %s",
		escapedUsername,
		escapedPassword,
	)

	cmd := exec.CommandContext(ctx, "mysql", "-e", sql)
	if err := cmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "failed to create user",
		}
	}

	return dbUserCreateResponse{OK: true}, nil
}

func init() {
	Default.Register("db_user.create", dbUserCreateHandler)
}
