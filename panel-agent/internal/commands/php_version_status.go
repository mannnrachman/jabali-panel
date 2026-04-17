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

// SupportedPHPVersions is the list of PHP versions that can be managed.
// Exported so API and other packages can reference it.
var SupportedPHPVersions = []string{"7.4", "8.0", "8.1", "8.2", "8.3", "8.4", "8.5"}

// phpVersionStatusResponse is the output shape for php.version.status.
type phpVersionStatusResponse struct {
	DefaultVersion string                      `json:"default_version"`
	Versions       []phpVersionStatusDetail     `json:"versions"`
}

// phpVersionStatusDetail represents the status of a single PHP version.
type phpVersionStatusDetail struct {
	Version    string `json:"version"`
	Installed  bool   `json:"installed"`
	FPMRunning bool   `json:"fpm_running"`
}

// isInstalledPHPVersion checks if a PHP version is installed by verifying the pool.d directory exists.
func isInstalledPHPVersion(version string) bool {
	poolDir := filepath.Join("/etc/php", version, "fpm/pool.d")
	info, err := os.Stat(poolDir)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// isFPMRunningPHPVersion checks if the PHP-FPM service for a version is running.
// For testing purposes, this can be swapped out via a wrapper function.
var checkFPMRunning = func(version string) bool {
	cmd := exec.Command("systemctl", "is-active", "--quiet", fmt.Sprintf("php%s-fpm.service", version))
	return cmd.Run() == nil
}

func phpVersionStatusHandler(ctx context.Context, params json.RawMessage) (any, error) {
	// No params expected for php.version.status.
	if params != nil && len(params) > 0 {
		var p map[string]interface{}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("failed to parse params: %v", err),
			}
		}
	}

	versions := make([]phpVersionStatusDetail, len(SupportedPHPVersions))
	for i, v := range SupportedPHPVersions {
		versions[i] = phpVersionStatusDetail{
			Version:    v,
			Installed:  isInstalledPHPVersion(v),
			FPMRunning: checkFPMRunning(v),
		}
	}

	return phpVersionStatusResponse{
		DefaultVersion: "8.5",
		Versions:       versions,
	}, nil
}

func init() {
	Default.Register("php.version.status", phpVersionStatusHandler)
}
