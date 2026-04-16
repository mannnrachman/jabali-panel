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

// userCreateParams is the input shape for user.create.
type userCreateParams struct {
	Username  string `json:"username"`
	HomeDir   string `json:"home_dir"`
	Shell     string `json:"shell"`
	Password  string `json:"password"`
}

// userCreateResponse is the output shape for user.create.
type userCreateResponse struct {
	Username string `json:"username"`
	UID      int    `json:"uid"`
	HomeDir  string `json:"home_dir"`
}

// usernameRegex validates POSIX username format.
var usernameRegex = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func userCreateHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p userCreateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate username format.
	if !usernameRegex.MatchString(p.Username) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid username %q: must match ^[a-z_][a-z0-9_-]{0,31}$", p.Username),
		}
	}

	// Validate home_dir starts with /home/.
	if !strings.HasPrefix(p.HomeDir, "/home/") {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid home_dir %q: must start with /home/", p.HomeDir),
		}
	}

	// Check if user already exists.
	checkCmd := exec.CommandContext(ctx, "id", p.Username)
	if err := checkCmd.Run(); err == nil {
		// User exists.
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeAlreadyExists,
			Message: fmt.Sprintf("user %q already exists", p.Username),
		}
	}

	// Create user with home directory.
	createCmd := exec.CommandContext(ctx, "useradd",
		"--create-home",
		"--home-dir", p.HomeDir,
		"--shell", p.Shell,
		p.Username,
	)
	if err := createCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("useradd failed: %v", err),
		}
	}

	// Set password if provided.
	if p.Password != "" {
		chpasswdCmd := exec.CommandContext(ctx, "chpasswd")
		chpasswdCmd.Stdin = strings.NewReader(p.Username + ":" + p.Password + "\n")
		if err := chpasswdCmd.Run(); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("chpasswd failed: %v", err),
			}
		}
	}

	// Chown home to <user>:www-data so nginx (running as www-data) can
	// read the docroot via group perms. Tenants stay isolated: other
	// regular users can't read /home/<user>.
	chownCmd := exec.CommandContext(ctx, "chown", p.Username+":www-data", p.HomeDir)
	if err := chownCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("chown failed: %v", err),
		}
	}

	// 0750: owner (user) rwx, group (www-data) rx, others nothing.
	chmodCmd := exec.CommandContext(ctx, "chmod", "0750", p.HomeDir)
	if err := chmodCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("chmod failed: %v", err),
		}
	}

	// Get UID.
	idCmd := exec.CommandContext(ctx, "id", "-u", p.Username)
	uidOutput, err := idCmd.Output()
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to get UID: %v", err),
		}
	}

	uid, err := strconv.Atoi(strings.TrimSpace(string(uidOutput)))
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to parse UID: %v", err),
		}
	}

	return userCreateResponse{
		Username: p.Username,
		UID:      uid,
		HomeDir:  p.HomeDir,
	}, nil
}

func init() {
	Default.Register("user.create", userCreateHandler)
}
