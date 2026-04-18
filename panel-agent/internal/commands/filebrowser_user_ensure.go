package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// filebrowserUserEnsureParams is the input shape for filebrowser.user.ensure.
type filebrowserUserEnsureParams struct {
	Username string `json:"username"`
	Scope    string `json:"scope"`
}

// filebrowserUserEnsureResponse is the output shape for filebrowser.user.ensure.
type filebrowserUserEnsureResponse struct {
	Username string `json:"username"`
	Scope    string `json:"scope"`
	Created  bool   `json:"created,omitempty"`
	NoChange bool   `json:"no_change,omitempty"`
}

var filebrowserUsernameRegex = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

const filebrowserDB = "/var/lib/jabali-filebrowser/filebrowser.db"

func filebrowserUserEnsureHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filebrowserUserEnsureParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate username format
	if !filebrowserUsernameRegex.MatchString(p.Username) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid username %q: must match ^[a-z][a-z0-9_]{0,31}$", p.Username),
		}
	}

	if p.Scope == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "scope cannot be empty",
		}
	}

	// Check if user already exists
	findCmd := exec.CommandContext(ctx, "filebrowser",
		"--database", filebrowserDB,
		"users", "find", p.Username)
	var findOut, findErr bytes.Buffer
	findCmd.Stdout = &findOut
	findCmd.Stderr = &findErr
	findExists := findCmd.Run() == nil

	if findExists {
		// User exists; no changes needed (idempotent)
		return &filebrowserUserEnsureResponse{
			Username: p.Username,
			Scope:    p.Scope,
			NoChange: true,
		}, nil
	}

	// Generate a random password (filebrowser will hash it, we don't actually use it
	// since auth is SSO via proxy auth)
	password := generateRandomPassword(16)

	// Create the user with specific permissions
	addCmd := exec.CommandContext(ctx, "filebrowser",
		"--database", filebrowserDB,
		"users", "add",
		p.Username, password,
		"--scope", p.Scope,
		"--perm.admin=false",
		"--perm.execute=false",
		"--perm.share=false",
		"--perm.create=true",
		"--perm.rename=true",
		"--perm.modify=true",
		"--perm.delete=true",
		"--perm.download=true",
	)

	var addOut, addErrBuf bytes.Buffer
	addCmd.Stdout = &addOut
	addCmd.Stderr = &addErrBuf

	if err := addCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to create filebrowser user: %v (stderr: %s)", err, addErrBuf.String()),
		}
	}

	return &filebrowserUserEnsureResponse{
		Username: p.Username,
		Scope:    p.Scope,
		Created:  true,
	}, nil
}

func init() {
	Default.Register("filebrowser.user.ensure", filebrowserUserEnsureHandler)
}
