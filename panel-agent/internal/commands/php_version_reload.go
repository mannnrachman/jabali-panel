package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// phpVersionReloadParams is the input shape for php.version.reload.
type phpVersionReloadParams struct {
	Version string `json:"version"`
}

// phpVersionReloadResponse is the output shape for php.version.reload.
type phpVersionReloadResponse struct {
	Version string `json:"version"`
	Message string `json:"message"`
}

func phpVersionReloadHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if params == nil || len(params) == 0 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "version parameter required",
		}
	}

	var p phpVersionReloadParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate version format
	if !versionRegex.MatchString(p.Version) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid version format: %q (expected X.Y)", p.Version),
		}
	}

	// Check if version is supported
	if !isVersionSupported(p.Version) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("unsupported version: %q", p.Version),
		}
	}

	// Reload the service
	serviceName := fmt.Sprintf("php%s-fpm.service", p.Version)
	cmd := exec.CommandContext(ctx, "systemctl", "reload", serviceName)
	if err := cmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to reload %s: %v", serviceName, err),
		}
	}

	return phpVersionReloadResponse{
		Version: p.Version,
		Message: "reload successful",
	}, nil
}

func init() {
	Default.Register("php.version.reload", phpVersionReloadHandler)
}
