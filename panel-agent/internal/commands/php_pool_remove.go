package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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

	// Remove pool files.
	for _, path := range matches {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to remove %s: %v", path, err),
			}
		}
	}

	// Remove per-user FPM config.
	fpmConfRoot := os.Getenv("JABALI_FPM_CONFIG_ROOT")
	if fpmConfRoot == "" {
		fpmConfRoot = "/etc/jabali-panel/fpm"
	}
	fpmConfPath := filepath.Join(fpmConfRoot, p.Username+".conf")
	if err := os.Remove(fpmConfPath); err != nil && !os.IsNotExist(err) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to remove per-user fpm config: %v", err),
		}
	}

	// Remove version pin file.
	verPinRoot := os.Getenv("JABALI_PHP_VER_PIN_ROOT")
	if verPinRoot == "" {
		verPinRoot = "/etc/jabali-panel/user-phpver"
	}
	verPinPath := filepath.Join(verPinRoot, p.Username)
	if err := os.Remove(verPinPath); err != nil && !os.IsNotExist(err) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to remove version pin: %v", err),
		}
	}

	// Stop the per-user FPM service (if loaded).
	if os.Getenv("JABALI_PHP_POOL_SKIP_RELOAD") == "" {
		serviceName := fmt.Sprintf("jabali-fpm@%s.service", p.Username)
		stopCmd := exec.CommandContext(ctx, "systemctl", "stop", serviceName)
		_ = stopCmd.Run() // Ignore error; unit may not be loaded.
	}

	return phpPoolRemoveResponse{
		Removed: len(matches),
	}, nil
}

func init() {
	Default.Register("php.pool.remove", phpPoolRemoveHandler)
}
