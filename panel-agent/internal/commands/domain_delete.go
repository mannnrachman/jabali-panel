package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// domainDeleteParams is the input shape for domain.delete.
type domainDeleteParams struct {
	Domain string `json:"domain"`
}

// domainDeleteResponse is the output shape for domain.delete.
type domainDeleteResponse struct {
	Domain  string `json:"domain"`
	Deleted bool   `json:"deleted"`
}

func domainDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p domainDeleteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate domain format
	if !domainRegex.MatchString(p.Domain) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid domain %q: must match ^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$", p.Domain),
		}
	}

	// Remove enabled symlink (ignore if missing)
	enabledPath := filepath.Join("/etc/nginx/sites-enabled", p.Domain+".conf")
	os.Remove(enabledPath)

	// Remove available config (ignore if missing)
	availablePath := filepath.Join("/etc/nginx/sites-available", p.Domain+".conf")
	os.Remove(availablePath)

	// Reload nginx
	reloadCmd := exec.CommandContext(ctx, "systemctl", "reload", "nginx")
	var reloadOutput bytes.Buffer
	reloadCmd.Stdout = &reloadOutput
	reloadCmd.Stderr = &reloadOutput
	if err := reloadCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("systemctl reload nginx failed: %s", reloadOutput.String()),
		}
	}

	return domainDeleteResponse{
		Domain:  p.Domain,
		Deleted: true,
	}, nil
}

func init() {
	Default.Register("domain.delete", domainDeleteHandler)
}
