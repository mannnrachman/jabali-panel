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

// disableDefaultPool moves the default www.conf pool to .disabled.
func disableDefaultPool(version string) {
	poolFile := filepath.Join("/etc/php", version, "fpm/pool.d/www.conf")
	if _, err := os.Stat(poolFile); err == nil {
		os.Rename(poolFile, poolFile+".disabled")
	}
}

// startAndEnableFPM ensures the FPM service is enabled and started.
func startAndEnableFPM(version string) error {
	serviceName := fmt.Sprintf("php%s-fpm.service", version)

	// Enable the service
	cmd := exec.Command("systemctl", "enable", "--quiet", serviceName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl enable failed: %w", err)
	}

	// Start the service
	cmd = exec.Command("systemctl", "start", serviceName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl start failed: %w; stderr: %s", err, stderr.String())
	}

	return nil
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

	// Disable the default pool
	disableDefaultPool(p.Version)

	// Enable and start the service
	if err := startAndEnableFPM(p.Version); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: err.Error(),
		}
	}

	// Small delay to allow service to stabilize
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
