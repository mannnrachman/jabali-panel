package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// phpVersionInstallParams is the input shape for php.version.install.
type phpVersionInstallParams struct {
	Version string `json:"version"`
}

// phpVersionInstallResponse is the output shape for php.version.install.
type phpVersionInstallResponse struct {
	Version    string `json:"version"`
	Installed  bool   `json:"installed"`
	FPMRunning bool   `json:"fpm_running"`
}

// versionRegex validates PHP version format: X.Y where X and Y are digits.
var versionRegex = regexp.MustCompile(`^\d+\.\d+$`)

// isVersionSupported checks if a version is in the supported list.
func isVersionSupported(version string) bool {
	for _, v := range SupportedPHPVersions {
		if v == version {
			return true
		}
	}
	return false
}

// isFPMAlreadyInstalledFunc is a function variable for testing.
var isFPMAlreadyInstalledFunc = func(version string) bool {
	cmd := exec.Command("command", "-v", fmt.Sprintf("php%s", version))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	return cmd.Run() == nil
}

// isFPMAlreadyInstalled checks if a PHP version is already installed.
func isFPMAlreadyInstalled(version string) bool {
	return isFPMAlreadyInstalledFunc(version)
}

// probePackage checks if an apt package exists via apt-cache show.
func probePackage(pkg string) bool {
	cmd := exec.Command("apt-cache", "show", pkg)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	return cmd.Run() == nil
}

// installPackages installs a list of apt packages with error handling.
func installPackages(pkgs []string) error {
	if len(pkgs) == 0 {
		return nil
	}

	cmd := exec.Command("apt-get", append(
		[]string{"install", "-y", "--no-install-recommends"},
		pkgs...,
	)...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr // capture both stdout and stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apt-get install failed: %w; output: %s", err, stderr.String())
	}

	return nil
}

// disableDefaultPool moves the default www.conf pool to .disabled. The
// distro-shipped www.conf references /run/php/php<v>-fpm.sock which
// conflicts with our per-user UDS sockets; we never want it active.
func disableDefaultPool(version string) {
	poolFile := filepath.Join("/etc/php", version, "fpm/pool.d/www.conf")
	if _, err := os.Stat(poolFile); err == nil {
		_ = os.Rename(poolFile, poolFile+".disabled")
	}
}

// placeholderPoolContent is what install.sh writes — mirrored here so
// the panel-driven install path ends in the exact same on-disk state as
// a fresh install.sh run. An empty pool.d/ would make FPM fail with
// "no pool defined"; the placeholder gives the global unit something
// parseable even though we mask the unit afterwards.
const placeholderPoolContent = `; Placeholder pool installed by jabali-agent so php-fpm has at least one
; valid pool. No-op ondemand pool listening on an unused loopback socket.
; Per ADR-0025 the global php<v>-fpm.service is masked in favour of
; per-user jabali-fpm@<user>.service masters.

[_jabali_placeholder]
user = www-data
group = www-data
listen = /run/php/php-fpm-jabali-placeholder.sock
listen.owner = www-data
listen.group = www-data
listen.mode = 0600
pm = ondemand
pm.max_children = 1
pm.process_idle_timeout = 10s
`

// installPlaceholderPool writes the placeholder pool file if it doesn't
// already exist. Idempotent; safe to call on every install.
func installPlaceholderPool(version string) error {
	path := filepath.Join("/etc/php", version, "fpm/pool.d/_jabali-placeholder.conf")
	if _, err := os.Stat(path); err == nil {
		return nil // already in place
	}
	// #nosec G306 — 0644 matches what install.sh writes; pool files must be world-readable to php-fpm.
	return os.WriteFile(path, []byte(placeholderPoolContent), 0o644)
}

// preMaskFPMService creates the /etc/systemd/system/php<v>-fpm.service
// mask symlink BEFORE apt installs the package. Writing the mask first
// means the postinst's `systemctl start` is a no-op instead of a
// failure — which was wedging dpkg on hosts where the service couldn't
// start (e.g. stale pool files from a prior half-configured install).
// The systemd unit directory needs to exist; it always does on Debian.
func preMaskFPMService(version string) error {
	serviceName := fmt.Sprintf("php%s-fpm.service", version)
	maskPath := filepath.Join("/etc/systemd/system", serviceName)
	// Remove any existing symlink/file so we can write fresh. Ignore
	// errors — a missing file is fine, and if we can't remove it the
	// symlink call below will surface the real problem.
	_ = os.Remove(maskPath)
	if err := os.Symlink("/dev/null", maskPath); err != nil {
		return fmt.Errorf("create mask symlink %s: %w", maskPath, err)
	}
	// daemon-reload so systemd picks up the new mask before apt's
	// postinst invokes systemctl.
	cmd := exec.Command("systemctl", "daemon-reload")
	_ = cmd.Run()
	return nil
}

// finalizeFPMMask runs after apt succeeds. reset-failed clears any
// residual failed state from a prior half-install, and a redundant
// `systemctl mask` call is a cheap idempotency check — if preMask
// failed for any reason, this catches it.
func finalizeFPMMask(version string) {
	serviceName := fmt.Sprintf("php%s-fpm.service", version)
	_ = exec.Command("systemctl", "reset-failed", serviceName).Run()
	_ = exec.Command("systemctl", "disable", "--quiet", serviceName).Run()
	_ = exec.Command("systemctl", "mask", "--quiet", serviceName).Run()
}

func phpVersionInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if params == nil || len(params) == 0 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "version parameter required",
		}
	}

	var p phpVersionInstallParams
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

	// If already installed, return current status
	if isFPMAlreadyInstalled(p.Version) {
		return phpVersionInstallResponse{
			Version:    p.Version,
			Installed:  isInstalledPHPVersion(p.Version),
			FPMRunning: checkFPMRunning(p.Version),
		}, nil
	}

	// Build package list
	required := []string{
		fmt.Sprintf("php%s-fpm", p.Version),
		fmt.Sprintf("php%s-cli", p.Version),
	}

	optionalNames := []string{"mysql", "mbstring", "zip", "gd", "curl", "xml", "intl", "bcmath", "opcache"}
	var optional []string
	for _, ext := range optionalNames {
		pkg := fmt.Sprintf("php%s-%s", p.Version, ext)
		if probePackage(pkg) {
			optional = append(optional, pkg)
		}
	}

	// Pre-mask the global php<v>-fpm.service BEFORE apt runs. The
	// distro postinst unconditionally `systemctl start`s the unit; if
	// it fails (stale pool files, binding conflict), dpkg marks the
	// package half-configured and subsequent apt transactions wedge on
	// it. Masking in advance turns the start into a no-op so apt
	// completes cleanly, and the mask is what we want anyway per
	// ADR-0025 (per-user jabali-fpm@<user>.service takes over).
	if err := preMaskFPMService(p.Version); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("pre-mask: %v", err),
		}
	}

	// Install all packages with context timeout
	pkgs := append(required, optional...)

	// Create a goroutine to install and signal completion or error
	done := make(chan error, 1)
	go func() {
		done <- installPackages(pkgs)
	}()

	// Wait for install to complete or context to cancel
	select {
	case <-ctx.Done():
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeDeadlineExceeded,
			Message: "installation timeout",
		}
	case err := <-done:
		if err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: err.Error(),
			}
		}
	}

	// Quiet the distro's default pool; install our placeholder so the
	// on-disk state matches a fresh install.sh run. Mask idempotently
	// in case pre-mask got rolled back by dpkg.
	disableDefaultPool(p.Version)
	if err := installPlaceholderPool(p.Version); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("install placeholder pool: %v", err),
		}
	}
	finalizeFPMMask(p.Version)

	// Small delay to let systemd settle after the mask/reset-failed
	// dance; callers query checkFPMRunning right after this and we
	// want a stable reading.
	time.Sleep(500 * time.Millisecond)

	return phpVersionInstallResponse{
		Version:    p.Version,
		Installed:  isInstalledPHPVersion(p.Version),
		FPMRunning: checkFPMRunning(p.Version),
	}, nil
}

func init() {
	Default.Register("php.version.install", phpVersionInstallHandler)
}
