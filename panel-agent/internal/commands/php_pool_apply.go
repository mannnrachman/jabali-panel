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
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"golang.org/x/sys/unix"
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

// phpVersionRegex validates PHP version format: X.Y where X and Y are digits.
var phpVersionRegex = regexp.MustCompile(`^\d+\.\d+$`)

// phpPoolUsernameRegex validates PHP pool username format: must start with lowercase
// letter, contain only lowercase letters, digits, underscores, max 32 chars.
var phpPoolUsernameRegex = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

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

// restartOrReloadUserFPM handles per-user FPM service restart/reload.
// If oldVersion == newVersion (and not empty), attempts reload via USR2.
// If the reload fails (unit not loaded/inactive), falls back to restart.
// On version change or first-time apply, does full restart.
// Also enables the service for auto-start on boot.
func restartOrReloadUserFPM(ctx context.Context, username string, oldVersion, newVersion string) error {
	// Skip systemctl operations in test environments.
	if os.Getenv("JABALI_PHP_POOL_SKIP_RELOAD") != "" {
		return nil
	}

	serviceName := fmt.Sprintf("jabali-fpm@%s.service", username)

	// Try reload if versions match and oldVersion is not empty.
	if oldVersion == newVersion && oldVersion != "" {
		reloadCmd := exec.CommandContext(ctx, "systemctl", "reload", serviceName)
		if err := reloadCmd.Run(); err != nil {
			// Reload failed; check if unit is not loaded or inactive, then restart.
			// Otherwise return the error.
			isActiveCmd := exec.CommandContext(ctx, "systemctl", "is-active", serviceName)
			if err := isActiveCmd.Run(); err != nil {
				// Unit not loaded or inactive; fall through to restart.
			} else {
				// Unit is active but reload failed — this is an error.
				return fmt.Errorf("failed to reload %s: %w", serviceName, err)
			}
		} else {
			// Reload succeeded; enable and return.
			_ = exec.CommandContext(ctx, "systemctl", "enable", "--quiet", serviceName).Run()
			return nil
		}
	}

	// Restart (version changed or first-time apply).
	restartCmd := exec.CommandContext(ctx, "systemctl", "restart", serviceName)
	if err := restartCmd.Run(); err != nil {
		return fmt.Errorf("failed to restart %s: %w", serviceName, err)
	}

	// Enable the service for auto-start on boot.
	enableCmd := exec.CommandContext(ctx, "systemctl", "enable", "--quiet", serviceName)
	if err := enableCmd.Run(); err != nil {
		return fmt.Errorf("failed to enable %s: %w", serviceName, err)
	}

	return nil
}

// readVersionPinFile reads the version pin from disk, or returns empty string if not found.
func readVersionPinFile(username string) (string, error) {
	verPinRoot := os.Getenv("JABALI_PHP_VER_PIN_ROOT")
	if verPinRoot == "" {
		verPinRoot = "/etc/jabali-panel/user-phpver"
	}
	verPinPath := filepath.Join(verPinRoot, username)
	data, err := os.ReadFile(verPinPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read version pin: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// writeVersionPinFile writes the version pin to disk.
func writeVersionPinFile(username, version string) error {
	verPinRoot := os.Getenv("JABALI_PHP_VER_PIN_ROOT")
	if verPinRoot == "" {
		verPinRoot = "/etc/jabali-panel/user-phpver"
	}
	if err := os.MkdirAll(verPinRoot, 0755); err != nil {
		return fmt.Errorf("failed to create version pin dir: %w", err)
	}
	verPinPath := filepath.Join(verPinRoot, username)
	if err := os.WriteFile(verPinPath, []byte(version+"\n"), 0644); err != nil {
		return fmt.Errorf("failed to write version pin: %w", err)
	}
	return nil
}

// writePerUserFPMConfig writes the per-user FPM config that includes only this user's pool.
func writePerUserFPMConfig(username, version string) error {
	fpmConfRoot := os.Getenv("JABALI_FPM_CONFIG_ROOT")
	if fpmConfRoot == "" {
		fpmConfRoot = "/etc/jabali-panel/fpm"
	}
	if err := os.MkdirAll(fpmConfRoot, 0755); err != nil {
		return fmt.Errorf("failed to create fpm config dir: %w", err)
	}

	fpmConfPath := filepath.Join(fpmConfRoot, username+".conf")
	poolConfigPath := fmt.Sprintf("/etc/php/%s/fpm/pool.d/jabali-%s.conf", version, username)

	confContent := fmt.Sprintf(`[global]
pid = /run/php/jabali-%s/fpm.pid
error_log = /var/log/php-fpm-%s.log
daemonize = no

; Include only this user's pool file — prevents multi-master-loads-all-pools bug.
include=%s
`, username, username, poolConfigPath)

	if err := os.WriteFile(fpmConfPath, []byte(confContent), 0644); err != nil {
		return fmt.Errorf("failed to write per-user fpm config: %w", err)
	}
	return nil
}

// acquireLock acquires an exclusive flock on a per-user lock file with a 30-second timeout.
func acquireLock(username string) (*os.File, error) {
	lockDir := "/run/jabali"
	if err := os.MkdirAll(lockDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create lock dir: %w", err)
	}

	lockPath := filepath.Join(lockDir, fmt.Sprintf("pool-apply-%s.lock", username))
	file, err := os.Create(lockPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create lock file: %w", err)
	}

	// Attempt to acquire exclusive lock with 30-second timeout.
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- unix.Flock(int(file.Fd()), unix.LOCK_EX)
	}()

	select {
	case err := <-doneCh:
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("failed to acquire flock: %w", err)
		}
		return file, nil
	case <-time.After(30 * time.Second):
		file.Close()
		return nil, fmt.Errorf("lock acquisition timeout (30s) — stuck apply?")
	}
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

	// Acquire per-user flock to serialize pool-apply operations for this user.
	lockFile, err := acquireLock(p.Username)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("flock acquisition failed: %v", err),
		}
	}
	defer lockFile.Close()

	// Read old version before making changes.
	oldVersion, err := readVersionPinFile(p.Username)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to read old version: %v", err),
		}
	}

	// Delete stale pool files and collect versions that need reload.
	_, err = globDeletePoolFiles(p.Username)
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
	// Socket lives in a user-owned subdir of /run/php (see fpm-pre-start).
	// Path is version-independent so nginx configs survive PHP version
	// switches without regeneration. One socket per user, not per pool.
	socketPath := fmt.Sprintf("/run/php/jabali-%s/fpm.sock", p.Username)
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

	// Write the per-user FPM config (includes only this user's pool).
	if err := writePerUserFPMConfig(p.Username, p.PHPVersion); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to write per-user fpm config: %v", err),
		}
	}

	// Write the version pin file.
	if err := writeVersionPinFile(p.Username, p.PHPVersion); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to write version pin: %v", err),
		}
	}

	// Restart or reload the per-user FPM service.
	if err := restartOrReloadUserFPM(ctx, p.Username, oldVersion, p.PHPVersion); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: err.Error(),
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
