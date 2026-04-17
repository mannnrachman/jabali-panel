package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// phpPoolApplyParams is the input shape for php.pool.apply.
type phpPoolApplyParams struct {
	Username                   string `json:"username"`
	PHPVersion                 string `json:"php_version"`
	PmMode                     string `json:"pm_mode"`
	PmMaxChildren              uint32 `json:"pm_max_children"`
	ProcessIdleTimeoutSeconds  uint32 `json:"process_idle_timeout_seconds"`
	AdminValues                []KV   `json:"admin_values"`
	AdminFlags                 []KV   `json:"admin_flags"`
}

// KV represents a key-value pair for ini directives.
type KV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// phpPoolApplyResponse is the output shape for php.pool.apply.
type phpPoolApplyResponse struct {
	SocketPath string `json:"socket_path"`
	PoolName   string `json:"pool_name"`
}

// phpPoolSpecTemplate represents the template data for rendering the pool config.
type phpPoolSpecTemplate struct {
	PoolName                       string
	User                           string
	Group                          string
	SocketPath                     string
	PmMode                         string
	PmMaxChildren                  uint32
	ProcessIdleTimeoutSeconds      uint32
	AdminValues                    []KV
	AdminFlags                     []KV
}

// phpPoolUsernameRegex validates PHP pool username format: must start with lowercase
// letter, contain only lowercase letters, digits, underscores, max 32 chars.
var phpPoolUsernameRegex = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// phpVersionRegex validates PHP version format: X.Y where X and Y are digits.
var phpVersionRegex = regexp.MustCompile(`^\d+\.\d+$`)

// adminValueAllowlist is the set of allowed php_admin_value directives.
var adminValueAllowlist = map[string]bool{
	"memory_limit":         true,
	"upload_max_filesize":  true,
	"post_max_size":        true,
	"max_execution_time":   true,
	"max_input_vars":       true,
	"max_input_time":       true,
	"date.timezone":        true,
}

// adminFlagAllowlist is the set of allowed php_admin_flag directives.
var adminFlagAllowlist = map[string]bool{
	"display_errors": true,
	"log_errors":     true,
	"file_uploads":   true,
}

// forbiddenDirectives are directives that must never appear in overrides,
// even if they pass the allowlist check. Belt-and-suspenders defense.
var forbiddenDirectives = map[string]bool{
	"open_basedir":       true,
	"disable_functions":  true,
	"extension_dir":      true,
	"zend_extension":     true,
}

// beltAndSuspendersCheck performs a final check on directive names to ensure
// no jailbreak-relevant directives slip through.
func isForbiddenDirective(name string) bool {
	if forbiddenDirectives[name] {
		return true
	}
	// Also reject any name containing a newline, regardless of allowlist.
	if strings.ContainsAny(name, "\n\r") {
		return true
	}
	return false
}

// globDeletePoolFiles removes all pool files for the given username across
// all installed PHP versions. Returns a map of PHP versions whose pool files
// were deleted (for subsequent reload).
func globDeletePoolFiles(username string) (map[string]bool, error) {
	pattern := fmt.Sprintf("/etc/php/*/fpm/pool.d/jabali-%s.conf", username)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob failed: %w", err)
	}

	deletedVersions := make(map[string]bool)
	for _, path := range matches {
		// Extract version from /etc/php/<version>/fpm/pool.d/...
		parts := strings.Split(path, "/")
		if len(parts) >= 3 && parts[1] == "etc" && parts[2] == "php" {
			version := parts[3]
			deletedVersions[version] = true
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to remove %s: %w", path, err)
		}
	}
	return deletedVersions, nil
}

// reloadFPMService reloads the given PHP-FPM service via systemctl.
// Skips reload if JABALI_PHP_POOL_SKIP_RELOAD env var is set (for testing).
func reloadFPMService(ctx context.Context, version string) error {
	// Skip reload in test environments.
	if os.Getenv("JABALI_PHP_POOL_SKIP_RELOAD") != "" {
		return nil
	}
	serviceName := fmt.Sprintf("php%s-fpm", version)
	cmd := exec.CommandContext(ctx, "systemctl", "reload", serviceName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to reload %s: %w", serviceName, err)
	}
	return nil
}

func phpPoolApplyHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p phpPoolApplyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate username.
	if !phpPoolUsernameRegex.MatchString(p.Username) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid username format",
		}
	}

	// Validate php_version format and check directory exists.
	if !phpVersionRegex.MatchString(p.PHPVersion) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid php_version format (expected X.Y)",
		}
	}

	poolDir := fmt.Sprintf("/etc/php/%s/fpm/pool.d/", p.PHPVersion)
	if info, err := os.Stat(poolDir); err != nil || !info.IsDir() {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("php version %s not installed", p.PHPVersion),
		}
	}

	// Validate pm_mode.
	pmModes := map[string]bool{"static": true, "ondemand": true, "dynamic": true}
	if !pmModes[p.PmMode] {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid pm_mode (must be static, ondemand, or dynamic)",
		}
	}

	// Validate pm_max_children.
	if p.PmMaxChildren == 0 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "pm_max_children must be > 0",
		}
	}

	// Validate process_idle_timeout_seconds.
	if p.ProcessIdleTimeoutSeconds == 0 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "process_idle_timeout_seconds must be > 0",
		}
	}

	// Validate admin_values directives.
	for _, av := range p.AdminValues {
		if isForbiddenDirective(av.Name) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("forbidden directive: %s", av.Name),
			}
		}
		if !adminValueAllowlist[av.Name] {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("unknown admin_value directive: %s", av.Name),
			}
		}
	}

	// Validate admin_flags directives and values.
	for _, af := range p.AdminFlags {
		if isForbiddenDirective(af.Name) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("forbidden directive: %s", af.Name),
			}
		}
		if !adminFlagAllowlist[af.Name] {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("unknown admin_flag directive: %s", af.Name),
			}
		}
		if af.Value != "on" && af.Value != "off" {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("admin_flag value must be 'on' or 'off', got: %s", af.Value),
			}
		}
	}

	// Delete stale pool files and collect versions that need reload.
	deletedVersions, err := globDeletePoolFiles(p.Username)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to clean stale pools: %v", err),
		}
	}

	// Build pool config path and socket path.
	// Support JABALI_PHP_POOL_CONFIG_DIR env var for testing.
	poolConfigDir := os.Getenv("JABALI_PHP_POOL_CONFIG_DIR")
	if poolConfigDir == "" {
		poolConfigDir = fmt.Sprintf("/etc/php/%s/fpm/pool.d", p.PHPVersion)
	}
	poolConfigPath := fmt.Sprintf("%s/jabali-%s.conf", poolConfigDir, p.Username)
	socketPath := fmt.Sprintf("/run/php/php%s-fpm-%s.sock", p.PHPVersion, p.Username)
	poolName := fmt.Sprintf("jabali-%s", p.Username)

	// Render the template.
	// Support JABALI_PHP_POOL_TEMPLATE_PATH env var for testing.
	tmplPath := os.Getenv("JABALI_PHP_POOL_TEMPLATE_PATH")
	if tmplPath == "" {
		tmplPath = "/etc/jabali-panel/php-pool.conf.tmpl"
	}
	tmplData, err := os.ReadFile(tmplPath)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to read pool template: %v", err),
		}
	}

	tmpl, err := template.New("pool").Parse(string(tmplData))
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to parse pool template: %v", err),
		}
	}

	spec := phpPoolSpecTemplate{
		PoolName:                  poolName,
		User:                      p.Username,
		Group:                     p.Username,
		SocketPath:                socketPath,
		PmMode:                    p.PmMode,
		PmMaxChildren:             p.PmMaxChildren,
		ProcessIdleTimeoutSeconds: p.ProcessIdleTimeoutSeconds,
		AdminValues:               p.AdminValues,
		AdminFlags:                p.AdminFlags,
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, spec); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to render pool template: %v", err),
		}
	}

	// Write the pool config file.
	if err := os.WriteFile(poolConfigPath, []byte(buf.String()), 0644); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to write pool config: %v", err),
		}
	}

	// Reload the target FPM service.
	if err := reloadFPMService(ctx, p.PHPVersion); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: err.Error(),
		}
	}

	// Also reload any previously deleted versions.
	for version := range deletedVersions {
		if version != p.PHPVersion {
			if err := reloadFPMService(ctx, version); err != nil {
				return nil, &agentwire.AgentError{
					Code:    agentwire.CodeInternal,
					Message: fmt.Sprintf("failed to reload previous version %s: %v", version, err),
				}
			}
		}
	}

	return phpPoolApplyResponse{
		SocketPath: socketPath,
		PoolName:   poolName,
	}, nil
}

func init() {
	Default.Register("php.pool.apply", phpPoolApplyHandler)
}
