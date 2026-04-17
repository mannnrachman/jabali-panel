package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// phpVersionListResponse is the output shape for php.version.list.
type phpVersionListResponse struct {
	Versions []string `json:"versions"`
}

// extractVersionFromPath extracts the PHP version from an FPM pool directory path.
// Given /etc/php/8.3/fpm/pool.d, returns "8.3".
func extractVersionFromPath(path string) (string, error) {
	parts := strings.Split(path, "/")
	// We expect parts[0]="", [1]="etc", [2]="php", [3]="VERSION", [4]="fpm", [5]="pool.d"
	if len(parts) >= 6 && parts[1] == "etc" && parts[2] == "php" && parts[4] == "fpm" && parts[5] == "pool.d" {
		return parts[3], nil
	}
	return "", fmt.Errorf("invalid path format: %s", path)
}

// listInstalledPHPVersions lists all installed PHP versions by reading /etc/php/*/fpm/pool.d directories.
func listInstalledPHPVersions() ([]string, error) {
	pattern := "/etc/php/*/fpm/pool.d"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob failed: %w", err)
	}

	versionsMap := make(map[string]bool)
	for _, path := range matches {
		// Verify the path actually exists and is a directory.
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}

		version, err := extractVersionFromPath(path)
		if err != nil {
			continue
		}
		versionsMap[version] = true
	}

	// Convert map to sorted slice.
	var versions []string
	for v := range versionsMap {
		versions = append(versions, v)
	}
	sort.Strings(versions)

	return versions, nil
}

func phpVersionListHandler(ctx context.Context, params json.RawMessage) (any, error) {
	// No params expected or required for php.version.list.
	if params != nil && len(params) > 0 {
		var p map[string]interface{}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("failed to parse params: %v", err),
			}
		}
		// Even if params are provided, we ignore them and proceed.
	}

	versions, err := listInstalledPHPVersions()
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to list PHP versions: %v", err),
		}
	}

	return phpVersionListResponse{
		Versions: versions,
	}, nil
}

func init() {
	Default.Register("php.version.list", phpVersionListHandler)
}
