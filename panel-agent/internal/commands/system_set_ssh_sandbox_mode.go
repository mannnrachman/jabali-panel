package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// system.set_ssh_sandbox_mode — atomically write the sandbox mode +
// optional default image to /etc/jabali. Wrapper reads on every connect
// so no service reload is needed.

type systemSetSSHSandboxModeParams struct {
	Mode         string `json:"mode"`                    // "bubblewrap" | "nspawn"
	DefaultImage string `json:"default_image,omitempty"` // optional; only updated if non-empty
}

type systemSetSSHSandboxModeResponse struct {
	Mode         string `json:"mode"`
	DefaultImage string `json:"default_image,omitempty"`
}

func systemSetSSHSandboxModeHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p systemSetSSHSandboxModeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}
	switch p.Mode {
	case "bubblewrap", "nspawn":
	default:
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("mode must be bubblewrap or nspawn, got %q", p.Mode),
		}
	}
	if p.DefaultImage != "" && !nspawnImageRe.MatchString(p.DefaultImage) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "default_image must match [a-z0-9-]+",
		}
	}

	if err := os.MkdirAll("/etc/jabali", 0o755); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("mkdir /etc/jabali: %v", err),
		}
	}
	if err := atomicWrite("/etc/jabali/ssh-sandbox-mode", p.Mode+"\n"); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: err.Error(),
		}
	}
	if err := os.Chmod("/etc/jabali/ssh-sandbox-mode", 0o644); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("chmod ssh-sandbox-mode: %v", err),
		}
	}

	if p.DefaultImage != "" {
		if err := atomicWrite("/etc/jabali/default-nspawn-image", p.DefaultImage+"\n"); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: err.Error(),
			}
		}
		if err := os.Chmod("/etc/jabali/default-nspawn-image", 0o644); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("chmod default-nspawn-image: %v", err),
			}
		}
	}

	return &systemSetSSHSandboxModeResponse{Mode: p.Mode, DefaultImage: p.DefaultImage}, nil
}

func init() {
	Default.Register("system.set_ssh_sandbox_mode", systemSetSSHSandboxModeHandler)
}
