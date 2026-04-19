package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type glpiInstallReq struct {
	AppType      string `json:"app_type"`
	OSUser       string `json:"os_user"`
	Docroot      string `json:"docroot"`
	Subdirectory string `json:"subdirectory"`
	SiteURL      string `json:"site_url"`
	UseWWW       bool   `json:"use_www"`
	DBName       string `json:"db_name"`
	DBUser       string `json:"db_user"`
	DBPassword   string `json:"db_password"`
	DBHost       string `json:"db_host"`
	AdminUser    string `json:"admin_user"`
	AdminPass    string `json:"admin_pass"`
	AdminEmail   string `json:"admin_email"`
	Language     string `json:"language"`
}

type glpiInstallResp struct {
	Version string `json:"version"`
}

// glpiVersion is the upstream GLPI release this build targets. Bump
// alongside glpiTarballSHA256 when moving to a new release.
//
// Releases: https://github.com/glpi-project/glpi/releases
const glpiVersion = "10.0.16"

var glpiTarballURL = fmt.Sprintf(
	"https://github.com/glpi-project/glpi/releases/download/%s/glpi-%s.tgz",
	glpiVersion, glpiVersion,
)

const glpiTarballSHA256 = ""

// glpiAdminUserPattern: GLPI usernames are alnum + dot/dash/underscore.
var glpiAdminUserPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,64}$`)

// glpiLanguagePattern matches the locale codes GLPI uses (en_GB, fr_FR,
// zh_CN, …).
var glpiLanguagePattern = regexp.MustCompile(`^[a-z]{2}_[A-Z]{2}$`)

func computeGLPIInstallPath(docroot, subdirectory string) string {
	if subdirectory == "" {
		return docroot
	}
	return filepath.Join(docroot, subdirectory)
}

func downloadGLPITarball(ctx context.Context, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, glpiTarballURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", glpiTarballURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", glpiTarballURL, resp.StatusCode)
	}
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	return nil
}

func verifyGLPISHA256(path string) error {
	if glpiTarballSHA256 == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, glpiTarballSHA256) {
		return fmt.Errorf("glpi tarball sha256 mismatch: got %s want %s", got, glpiTarballSHA256)
	}
	return nil
}

// extractGLPITarball untars glpi-X.Y.Z.tgz. The tarball wraps content
// under a top-level "glpi/" directory; --strip-components=1 flattens.
func extractGLPITarball(ctx context.Context, osUser, tarballPath, installPath string) error {
	cmd := buildSystemdRunCmd(ctx, osUser,
		"tar",
		"--extract",
		"--gzip",
		"--strip-components=1",
		"--file", tarballPath,
		"--directory", installPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tar extract: %w (output: %s)", err, truncateStr(string(out), 512))
	}
	return nil
}

// runGLPIDatabaseInstall drives `bin/console glpi:database:install`.
// Materialises the schema with default users (glpi, tech, normal,
// post-only) — those default credentials are rotated/disabled in the
// next step.
func runGLPIDatabaseInstall(ctx context.Context, req glpiInstallReq, installPath string) error {
	dbHost := req.DBHost
	if dbHost == "" {
		dbHost = "localhost"
	}
	lang := req.Language
	if lang == "" {
		lang = "en_GB"
	}

	args := []string{
		"php", filepath.Join(installPath, "bin", "console"),
		"glpi:database:install",
		"--no-interaction",
		"--reconfigure",
		"--db-host=" + dbHost,
		"--db-name=" + req.DBName,
		"--db-user=" + req.DBUser,
		"--db-password=" + req.DBPassword,
		"--default-language=" + lang,
	}
	cmd := buildSystemdRunCmd(ctx, req.OSUser, args...)
	cmd.Dir = installPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bin/console glpi:database:install: %w (output: %s)", err, truncateStr(string(out), 1024))
	}
	return nil
}

// glpiPostInstallSecurityScript is the inline PHP script we run after
// glpi:database:install to clean up the security mess GLPI ships with:
//
//   1. Rename the default 'glpi' super-admin to the operator-supplied
//      admin_user, set its password to admin_pass, and update its email
//      via the linked glpi_useremails row.
//   2. Disable the other default accounts (tech, normal, post-only)
//      which all ship with username==password.
//
// Embedded as a string and written to /tmp at install time so we can
// run it with the per-domain user's php-cli without leaving any
// long-lived script on disk. password_hash is preferred over GLPI's
// own User::preparePassword to keep this script independent of GLPI's
// internal API (which differs across 10.0.x point releases).
const glpiPostInstallSecurityScript = `<?php
$argv = $_SERVER['argv'];
if (count($argv) < 8) {
    fwrite(STDERR, "usage: glpi-secure.php <dbhost> <dbuser> <dbpass> <dbname> <newadmin> <newpass> <newemail>\n");
    exit(2);
}
list(, $h, $u, $p, $d, $newAdmin, $newPass, $newEmail) = $argv;
$mysqli = @new mysqli($h, $u, $p, $d);
if ($mysqli->connect_errno) {
    fwrite(STDERR, "connect: " . $mysqli->connect_error . "\n");
    exit(3);
}
$hash = password_hash($newPass, PASSWORD_BCRYPT);
// Rename + reset password for the default super-admin (id == 2 in a
// fresh GLPI install; we match by name to avoid coupling to that).
$stmt = $mysqli->prepare("UPDATE glpi_users SET name=?, password=? WHERE name='glpi'");
$stmt->bind_param("ss", $newAdmin, $hash);
if (!$stmt->execute()) {
    fwrite(STDERR, "rotate glpi user: " . $stmt->error . "\n");
    exit(4);
}
// Update or insert the user's email row.
$uid = 0;
$res = $mysqli->query("SELECT id FROM glpi_users WHERE name=" . "'" . $mysqli->real_escape_string($newAdmin) . "'");
if ($res && ($row = $res->fetch_assoc())) {
    $uid = (int)$row['id'];
}
if ($uid > 0) {
    $eStmt = $mysqli->prepare("REPLACE INTO glpi_useremails (users_id, email, is_default) VALUES (?, ?, 1)");
    $eStmt->bind_param("is", $uid, $newEmail);
    $eStmt->execute();
}
// Disable the other default accounts that ship with username==password.
$mysqli->query("UPDATE glpi_users SET is_active = 0 WHERE name IN ('tech', 'normal', 'post-only')");
echo "ok\n";
`

// runGLPIPostInstallSecurity writes the security-cleanup PHP script
// to a per-install /tmp file, runs it with the per-domain user's
// php-cli (so the connection from the user's POV matches what the
// app will use at runtime), and removes the script.
func runGLPIPostInstallSecurity(ctx context.Context, req glpiInstallReq, installPath string) error {
	dbHost := req.DBHost
	if dbHost == "" {
		dbHost = "localhost"
	}

	scriptDir, err := os.MkdirTemp("", "glpi-secure-")
	if err != nil {
		return fmt.Errorf("mktemp script dir: %w", err)
	}
	defer os.RemoveAll(scriptDir)
	if err := os.Chmod(scriptDir, 0o755); err != nil {
		return fmt.Errorf("chmod script dir: %w", err)
	}
	scriptPath := filepath.Join(scriptDir, "secure.php")
	if err := os.WriteFile(scriptPath, []byte(glpiPostInstallSecurityScript), 0o644); err != nil {
		return fmt.Errorf("write secure.php: %w", err)
	}

	cmd := buildSystemdRunCmd(ctx, req.OSUser,
		"php", scriptPath,
		dbHost, req.DBUser, req.DBPassword, req.DBName,
		req.AdminUser, req.AdminPass, req.AdminEmail,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("post-install security script: %w (output: %s)", err, truncateStr(string(out), 512))
	}
	return nil
}

// removeGLPIInstallDir deletes the install/ folder after install.
// GLPI's runtime checks for it and surfaces a "remove install dir"
// warning otherwise.
func removeGLPIInstallDir(ctx context.Context, osUser, installPath string) error {
	cmd := buildSystemdRunCmd(ctx, osUser, "rm", "-rf", filepath.Join(installPath, "install"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rm install/: %w (output: %s)", err, truncateStr(string(out), 256))
	}
	return nil
}

func glpiInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req glpiInstallReq
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("failed to parse params: %v", err)}
	}
	if req.OSUser == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "os_user is required"}
	}
	if req.Docroot == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "docroot is required"}
	}
	if req.DBName == "" || req.DBUser == "" || req.DBPassword == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "db_name, db_user, db_password are required"}
	}
	if req.AdminUser == "" || !glpiAdminUserPattern.MatchString(req.AdminUser) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_user must be 3-64 chars of letters, digits, dot, dash, or underscore",
		}
	}
	if len(req.AdminPass) < 8 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_pass must be at least 8 characters",
		}
	}
	if req.AdminEmail == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "admin_email is required"}
	}
	if req.Language != "" && !glpiLanguagePattern.MatchString(req.Language) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("language %q must match e.g. en_GB", req.Language),
		}
	}
	if err := validateDocrootPath(req.OSUser, req.Docroot); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	installPath := computeGLPIInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		mkdirCmd := buildSystemdRunCmd(ctx, req.OSUser, "mkdir", "-p", installPath)
		if out, err := mkdirCmd.CombinedOutput(); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mkdir %s: %v (output: %s)", installPath, err, truncateStr(string(out), 256))}
		}
	}

	removePlaceholderIndex(ctx, installPath)

	tmpDir, err := os.MkdirTemp("", "glpi-")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mktemp: %v", err)}
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tmpdir: %v", err)}
	}
	tarballPath := filepath.Join(tmpDir, "glpi.tgz")

	dlCtx, dlCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer dlCancel()
	if err := downloadGLPITarball(dlCtx, tarballPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := verifyGLPISHA256(tarballPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := os.Chmod(tarballPath, 0o644); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tarball: %v", err)}
	}

	if err := extractGLPITarball(ctx, req.OSUser, tarballPath, installPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	installCtx, installCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer installCancel()
	if err := runGLPIDatabaseInstall(installCtx, req, installPath); err != nil {
		_ = exec.CommandContext(ctx, "rm", "-f", filepath.Join(installPath, "config", "config_db.php")).Run()
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := runGLPIPostInstallSecurity(ctx, req, installPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := removeGLPIInstallDir(ctx, req.OSUser, installPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := normalizePermsToWwwData(ctx, installPath, req.OSUser); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	return glpiInstallResp{Version: glpiVersion}, nil
}

func init() {
	RegisterAppInstaller("glpi", glpiInstallHandler)
}
