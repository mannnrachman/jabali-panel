package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// phpPoolRemoveParams is the input shape for php.pool.remove.
type phpPoolRemoveParams struct {
	Username string `json:"username"`
}

// phpPoolRemoveResponse is the output shape for php.pool.remove.
type phpPoolRemoveResponse struct {
	Removed int `json:"removed"`
}

func phpPoolRemoveHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p phpPoolRemoveParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate username format.
	if !phpPoolUsernameRegex.MatchString(p.Username) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid username format",
		}
	}

	// Glob delete all pool files for this username across all versions.
	pattern := fmt.Sprintf("/etc/php/*/fpm/pool.d/jabali-%s.conf", p.Username)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("glob failed: %v", err),
		}
	}

	// Collect versions that need reload, then remove files.
	versionsToReload := make(map[string]bool)
	for _, path := range matches {
		// Extract version from /etc/php/<version>/fpm/pool.d/...
		parts := strings.Split(path, "/")
		if len(parts) >= 3 && parts[1] == "etc" && parts[2] == "php" {
			version := parts[3]
			versionsToReload[version] = true
		}

		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to remove %s: %v", path, err),
			}
		}
	}

	// Reload all affected FPM services.
	for version := range versionsToReload {
		if err := reloadFPMService(ctx, version); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: err.Error(),
			}
		}
	}

	return phpPoolRemoveResponse{
		Removed: len(matches),
	}, nil
}

func init() {
	Default.Register("php.pool.remove", phpPoolRemoveHandler)
}
