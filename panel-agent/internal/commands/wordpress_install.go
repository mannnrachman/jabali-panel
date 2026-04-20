package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// wordpressInstallReq is the input shape for wordpress.install.
type wordpressInstallReq struct {
	OSUser       string `json:"os_user"`       // domain owner (e.g. "shuki")
	Docroot      string `json:"docroot"`       // /home/shuki/domains/example.com/public_html
	DBName       string `json:"db_name"`       // already-provisioned
	DBUser       string `json:"db_user"`       // already-provisioned
	DBPassword   string `json:"db_password"`   // plaintext, single-use
	DBHost       string `json:"db_host"`       // "localhost" (unix socket) or "127.0.0.1"
	SiteURL      string `json:"site_url"`      // https://example.com
	SiteTitle    string `json:"site_title"`
	AdminUser    string `json:"admin_user"`
	AdminPass    string `json:"admin_pass"`
	AdminEmail   string `json:"admin_email"`
	Locale       string `json:"locale"`
	UseWWW       bool   `json:"use_www"`       // prepend www. to domain in siteurl
	Subdirectory string `json:"subdirectory"`  // install in subdirectory (optional)
}

// wordpressInstallResp is the output shape for wordpress.install.
type wordpressInstallResp struct {
	Version string `json:"version"` // what wp-cli actually installed
}

// validateDocrootPath ensures the docroot is within /home/<osuser>/domains/
func validateDocrootPath(osUser, docroot string) error {
	allowedPrefix := filepath.Join("/home", osUser, "domains")
	absDocroot, err := filepath.Abs(docroot)
	if err != nil {
		return fmt.Errorf("failed to resolve docroot path: %v", err)
	}
	// Ensure absDocroot is under allowedPrefix
	relPath, err := filepath.Rel(allowedPrefix, absDocroot)
	if err != nil {
		return fmt.Errorf("docroot not in allowed path: %v", err)
	}
	// Check for path traversal (relPath containing ..)
	if strings.HasPrefix(relPath, "..") || strings.HasPrefix(relPath, "/") {
		return fmt.Errorf("docroot path traversal detected")
	}
	return nil
}

// buildSystemdRunCmd wraps a command in systemd-run for the given user/slice.
//
// `--quiet` suppresses systemd-run's own "Running as unit: ..." chatter
// on stderr — when callers pipe CombinedOutput into a downstream
// command (e.g. `find ... | xargs php`), that chatter contaminates
// the path. It also makes failure-error messages 90% shorter so
// LastError stays readable at the column's 1024-byte cap.
func buildSystemdRunCmd(ctx context.Context, osUser string, args ...string) *exec.Cmd {
	cmdArgs := []string{
		"systemd-run",
		"--quiet",
		"--uid=" + osUser,
		"--gid=" + osUser,
		"--slice=jabali-user-" + osUser + ".slice",
		"--pipe",
		"--wait",
		"--collect",
	}
	cmdArgs = append(cmdArgs, args...)
	return exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
}

// removePlaceholderIndex deletes index.html at the install root if present.
// domain.create writes a "this domain is provisioned" placeholder there;
// nginx's `index index.html index.php` directive serves it before WP's
// index.php, so it must go before the install completes — otherwise the
// browser keeps seeing the placeholder until the user manually removes it.
//
// Safe to call unconditionally: the install API rejects re-installs (DB
// idempotency check) so the only index.html the user could lose here is
// either the placeholder or a hand-uploaded file the user is intentionally
// replacing by clicking "Install WordPress" on this domain.
func removePlaceholderIndex(ctx context.Context, installPath string) {
	_ = exec.CommandContext(ctx, "rm", "-f", filepath.Join(installPath, "index.html")).Run()
}

// normalizePermsToWwwData makes the WP tree match the project's ownership
// convention used by domain.create: owner=<user>, group=www-data,
// dirs 0750, files 0640. nginx (in the www-data group) traverses via
// group bits; FPM (running AS the user) writes via owner bits. Without
// this step `wp core download` leaves files owned <user>:<user> with
// world-readable perms — works by accident today but breaks the moment
// any plugin or upload lands a 0700 dir or removes the world bit.
func normalizePermsToWwwData(ctx context.Context, installPath, osUser string) error {
	if err := exec.CommandContext(ctx, "chown", "-R", osUser+":www-data", installPath).Run(); err != nil {
		return fmt.Errorf("chown -R %s:www-data %s: %w", osUser, installPath, err)
	}
	if err := exec.CommandContext(ctx, "find", installPath, "-type", "d", "-exec", "chmod", "0750", "{}", "+").Run(); err != nil {
		return fmt.Errorf("chmod dirs 0750 under %s: %w", installPath, err)
	}
	if err := exec.CommandContext(ctx, "find", installPath, "-type", "f", "-exec", "chmod", "0640", "{}", "+").Run(); err != nil {
		return fmt.Errorf("chmod files 0640 under %s: %w", installPath, err)
	}
	return nil
}

// cleanupWordPressFiles performs best-effort cleanup on failure.
func cleanupWordPressFiles(ctx context.Context, docroot string) error {
	files := []string{
		"wp-config.php",
		"wp-config-sample.php",
		"wp-blog-header.php",
		"wp-load.php",
		"wp-login.php",
		"wp-settings.php",
		"wp-admin",
		"wp-content",
		"wp-includes",
		"readme.html",
		"license.txt",
		"index.php",
	}
	for _, file := range files {
		path := filepath.Join(docroot, file)
		// Use rm via exec to match the documented behavior
		cmd := exec.CommandContext(ctx, "rm", "-rf", path)
		// Ignore errors; best-effort cleanup
		_ = cmd.Run()
	}
	return nil
}

func wordpressInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req wordpressInstallReq
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate required fields
	if req.OSUser == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "os_user is required",
		}
	}
	if req.Docroot == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "docroot is required",
		}
	}
	if req.DBName == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "db_name is required",
		}
	}
	if req.DBUser == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "db_user is required",
		}
	}
	if req.DBPassword == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "db_password is required",
		}
	}
	if req.DBHost == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "db_host is required",
		}
	}
	if req.SiteURL == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "site_url is required",
		}
	}
	if req.AdminUser == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_user is required",
		}
	}
	if req.AdminPass == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_pass is required",
		}
	}
	if req.AdminEmail == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_email is required",
		}
	}

	// Default locale if not provided
	if req.Locale == "" {
		req.Locale = "en_US"
	}

	// Validate docroot is within /home/<osuser>/domains/
	if err := validateDocrootPath(req.OSUser, req.Docroot); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid docroot: %v", err),
		}
	}

	// Compute installPath: docroot + optional subdirectory
	installPath := req.Docroot
	if req.Subdirectory != "" {
		installPath = filepath.Join(req.Docroot, req.Subdirectory)
		// Create the subdirectory if it doesn't exist
		mkdirCmd := buildSystemdRunCmd(ctx,
			req.OSUser,
			"mkdir", "-p", installPath,
		)
		if err := mkdirCmd.Run(); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to create subdirectory: %v", err),
			}
		}
	}

	// Clean any WordPress leftovers from a previous failed attempt.
	// wp-cli refuses to download into a directory it thinks already
	// hosts WordPress, so retries would permanently fail unless we
	// clear stale wp-* files first. Idempotent: no-op on empty dir.
	_ = cleanupWordPressFiles(ctx, installPath)

	// Drop the domain.create placeholder index.html. nginx's index
	// directive lists html before php, so leaving it would mask WP's
	// index.php on /. The user clicked "Install WordPress" — that's
	// explicit intent to replace the docroot's landing page.
	removePlaceholderIndex(ctx, installPath)

	// Step 1: wp core download
	downloadCmd := buildSystemdRunCmd(ctx,
		req.OSUser,
		"wp", "core", "download",
		"--path="+installPath,
		"--locale="+req.Locale,
		"--version=latest",
	)
	var dlStdout, dlStderr bytes.Buffer
	downloadCmd.Stdout = &dlStdout
	downloadCmd.Stderr = &dlStderr
	if err := downloadCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("wp core download failed: %v; stderr=%q; stdout=%q",
				err,
				truncateStr(dlStderr.String(), 400),
				truncateStr(dlStdout.String(), 200),
			),
		}
	}

	// Step 2: wp config create (with placeholder password, then rewrite)
	configCmd := buildSystemdRunCmd(ctx,
		req.OSUser,
		"wp", "config", "create",
		"--path="+installPath,
		"--dbname="+req.DBName,
		"--dbuser="+req.DBUser,
		"--dbhost="+req.DBHost,
		"--dbpass=__JABALI_PLACEHOLDER__",
		"--dbcharset=utf8mb4",
		"--dbcollate=utf8mb4_unicode_ci",
		"--skip-check",
	)
	var ccStdout, ccStderr bytes.Buffer
	configCmd.Stdout = &ccStdout
	configCmd.Stderr = &ccStderr
	if err := configCmd.Run(); err != nil {
		_ = cleanupWordPressFiles(ctx, installPath)
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("wp config create failed: %v; stderr=%q; stdout=%q",
				err,
				truncateStr(ccStderr.String(), 400),
				truncateStr(ccStdout.String(), 200),
			),
		}
	}

	// Read wp-config.php and replace placeholder with real password
	configPath := filepath.Join(installPath, "wp-config.php")
	configContent, err := os.ReadFile(configPath)
	if err != nil {
		_ = cleanupWordPressFiles(ctx, installPath)
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to read wp-config.php: %v", err),
		}
	}

	// Replace the placeholder with the real password
	configContent = []byte(strings.ReplaceAll(
		string(configContent),
		"__JABALI_PLACEHOLDER__",
		req.DBPassword,
	))

	// Write back with restricted permissions (0640)
	if err := os.WriteFile(configPath, configContent, 0640); err != nil {
		_ = cleanupWordPressFiles(ctx, installPath)
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to write wp-config.php: %v", err),
		}
	}

	// Chown the config file to <user>:www-data so it matches the rest
	// of the docroot once normalizePermsToWwwData runs at the end.
	// Group=www-data lets nginx (in www-data) read the file via group bits;
	// FPM (running as <user>) reads via owner bits.
	chownCmd := exec.CommandContext(ctx, "chown", req.OSUser+":www-data", configPath)
	if err := chownCmd.Run(); err != nil {
		_ = cleanupWordPressFiles(ctx, installPath)
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("chown wp-config.php failed: %v", err),
		}
	}

	// Step 3: wp core install (with stdin-based admin password)
	installCmd := buildSystemdRunCmd(ctx,
		req.OSUser,
		"wp", "core", "install",
		"--path="+installPath,
		"--url="+req.SiteURL,
		"--title="+req.SiteTitle,
		"--admin_user="+req.AdminUser,
		"--admin_email="+req.AdminEmail,
		"--skip-email",
		"--prompt=admin_password",
	)
	// Pass admin password via stdin
	installCmd.Stdin = strings.NewReader(req.AdminPass + "\n")

	var installStdout, installStderr bytes.Buffer
	installCmd.Stdout = &installStdout
	installCmd.Stderr = &installStderr

	if err := installCmd.Run(); err != nil {
		_ = cleanupWordPressFiles(ctx, installPath)
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("wp core install failed: %v; stderr=%q; stdout=%q",
				err,
				truncateStr(installStderr.String(), 400),
				truncateStr(installStdout.String(), 200),
			),
		}
	}

	// Step 4: Get WordPress version
	versionCmd := buildSystemdRunCmd(ctx,
		req.OSUser,
		"wp", "core", "version",
		"--path="+installPath,
	)

	var versionOutput bytes.Buffer
	versionCmd.Stdout = &versionOutput
	versionCmd.Stderr = io.Discard

	if err := versionCmd.Run(); err != nil {
		_ = cleanupWordPressFiles(ctx, installPath)
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("wp core version failed: %v", err),
		}
	}

	version := strings.TrimSpace(versionOutput.String())

	// Normalize ownership + perms across the entire WP tree to the
	// project's <user>:www-data 0750/0640 convention. wp-cli ran under
	// systemd-run --uid=user --gid=user, so files landed as user:user
	// with the user's default umask — diverges from domain.create's
	// docroot ownership and breaks any nginx access path that depends
	// on group bits (e.g. a future plugin that creates a 0700 dir).
	if err := normalizePermsToWwwData(ctx, installPath, req.OSUser); err != nil {
		// Don't roll back the install — files are valid, perms are just
		// off. Surface the error so the panel marks the install as
		// having a recoverable issue rather than silently leaving the
		// docroot in the wrong shape.
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("normalize perms failed: %v", err),
		}
	}

	return wordpressInstallResp{
		Version: version,
	}, nil
}

func init() {
	// Legacy command — still registered so any straggler caller keeps
	// working through the M19 release window. M19.1 deletes this line.
	Default.Register("wordpress.install", wordpressInstallHandler)
	// M19 dispatch table: lets the panel call app.install with
	// app_type="wordpress" (see panel-agent/internal/commands/app_dispatch.go).
	RegisterAppInstaller("wordpress", wordpressInstallHandler)
}

// truncateStr trims s to at most n runes, appending "…" when truncated.
// Used to keep error messages small enough to fit in panel_api log
// fields and DB columns.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
