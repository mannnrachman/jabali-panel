package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// systemSetSSHConfigParams is the input shape for system.set_ssh_config.
type systemSetSSHConfigParams struct {
	Port            uint16 `json:"port"`
	PasswordAuth    bool   `json:"password_auth"`
}

// systemSetSSHConfigResponse is the output shape. Returns the port and auth
// setting that were actually applied.
type systemSetSSHConfigResponse struct {
	Port         uint16 `json:"port"`
	PasswordAuth bool   `json:"password_auth"`
}

// sshConfigPath is the drop-in config file for sshd. Can be overridden for testing.
func getSSHConfigPath() string {
	if p := os.Getenv("JABALI_SSHD_DROPIN_PATH"); p != "" {
		return p
	}
	return "/etc/ssh/sshd_config.d/jabali-sshd.conf"
}

// systemSetSSHConfigHandler applies SSH port and password auth settings to the OS
// via atomic drop-in file writes with validation (sshd -t) before applying.
// On validation failure, the previous config is restored.
func systemSetSSHConfigHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p systemSetSSHConfigParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate port range
	if p.Port < 1 || p.Port > 65535 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "port must be between 1 and 65535",
		}
	}

	configPath := getSSHConfigPath()

	// Read existing config (if any) so we can restore on failure
	prevContent, _ := os.ReadFile(configPath)

	// Build new config content
	passwordAuthStr := "no"
	if p.PasswordAuth {
		passwordAuthStr = "yes"
	}
	newContent := fmt.Sprintf("Port %d\nPasswordAuthentication %s\n", p.Port, passwordAuthStr)

	// Atomic write: write to .new, then rename
	newPath := configPath + ".new"
	if err := os.WriteFile(newPath, []byte(newContent), 0600); err != nil {
		return nil, fmt.Errorf("write sshd_config drop-in: %w", err)
	}

	if err := os.Rename(newPath, configPath); err != nil {
		_ = os.Remove(newPath) // best-effort cleanup
		return nil, fmt.Errorf("rename sshd_config drop-in: %w", err)
	}

	// Validate the new config with sshd -t
	skipValidate := os.Getenv("JABALI_SSHD_TEST_SKIP_VALIDATE") != ""
	if !skipValidate {
		cmd := exec.CommandContext(ctx, "sshd", "-t")
		if out, err := cmd.CombinedOutput(); err != nil {
			// Validation failed; restore previous config
			if len(prevContent) > 0 {
				_ = os.WriteFile(configPath, prevContent, 0600)
			} else {
				_ = os.Remove(configPath)
			}
			return nil, fmt.Errorf("sshd -t validation failed: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}

	// Reload sshd with the new config
	skipReload := os.Getenv("JABALI_SSHD_TEST_SKIP_RELOAD") != ""
	if !skipReload {
		cmd := exec.CommandContext(ctx, "systemctl", "reload", "sshd")
		if out, err := cmd.CombinedOutput(); err != nil {
			// Log but don't fail; config is already in place
			return nil, fmt.Errorf("systemctl reload sshd: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}

	return systemSetSSHConfigResponse{Port: p.Port, PasswordAuth: p.PasswordAuth}, nil
}

func init() {
	Default.Register("system.set_ssh_config", systemSetSSHConfigHandler)
}
