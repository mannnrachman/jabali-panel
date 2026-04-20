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

type backdropInstallReq struct {
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
	SiteTitle    string `json:"site_title"`
	AdminUser    string `json:"admin_user"`
	AdminPass    string `json:"admin_pass"`
	AdminEmail   string `json:"admin_email"`
	Profile      string `json:"profile"`
}

type backdropInstallResp struct {
	Version string `json:"version"`
}

// backdropVersion is the upstream Backdrop CMS release this build
// targets. Bump alongside backdropZipSHA256 when moving to a new
// release.
//
// Releases: https://github.com/backdrop/backdrop/releases
const backdropVersion = "1.28.4"

var backdropZipURL = fmt.Sprintf(
	"https://github.com/backdrop/backdrop/releases/download/%s/backdrop.zip",
	backdropVersion,
)

const backdropZipSHA256 = ""

// beeVersion is the bee CLI tool release. bee is the drush equivalent
// for Backdrop — single-file PHP script the agent downloads per
// install (no system-wide install needed). Pinned to a known-working
// version to avoid silent breakage from upstream changes.
//
// Tag format is `1.x-1.X.Y` (Backdrop-contrib branch+version), not
// bare `1.X.Y`, so the URL below uses the full tag string. The
// release ships an asset zip named `bee.zip` directly under
// /releases/download/<tag>/ — no archive/refs/tags layout, the
// auto-source archive at that URL would 404.
//
// Releases: https://github.com/backdrop-contrib/bee/releases
const beeVersion = "1.x-1.2.0"

var beeZipURL = fmt.Sprintf(
	"https://github.com/backdrop-contrib/bee/releases/download/%s/bee.zip",
	beeVersion,
)

const beeZipSHA256 = ""

var backdropAdminUserPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,60}$`)

var backdropProfilePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,49}$`)

func computeBackdropInstallPath(docroot, subdirectory string) string {
	if subdirectory == "" {
		return docroot
	}
	return filepath.Join(docroot, subdirectory)
}

func downloadBackdropZip(ctx context.Context, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, backdropZipURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", backdropZipURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", backdropZipURL, resp.StatusCode)
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

func verifyBackdropSHA256(path string) error {
	if backdropZipSHA256 == "" {
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
	if !strings.EqualFold(got, backdropZipSHA256) {
		return fmt.Errorf("backdrop zip sha256 mismatch: got %s want %s", got, backdropZipSHA256)
	}
	return nil
}

// downloadBee fetches the bee CLI tool zip into dest. bee ships as a
// github tarball; we download per-install rather than caching system-
// wide so a bee version bump doesn't silently affect existing apps.
func downloadBee(ctx context.Context, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, beeZipURL, nil)
	if err != nil {
		return fmt.Errorf("build bee request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download bee: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download bee: HTTP %d", resp.StatusCode)
	}
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create bee dest: %w", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("write bee: %w", err)
	}
	return nil
}

func verifyBeeSHA256(path string) error {
	if beeZipSHA256 == "" {
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
	if !strings.EqualFold(got, beeZipSHA256) {
		return fmt.Errorf("bee zip sha256 mismatch: got %s want %s", got, beeZipSHA256)
	}
	return nil
}

// extractBackdropZip unzips backdrop.zip into a staging dir. The zip
// wraps content under a top-level `backdrop/` directory; we cp -a
// the contents into installPath.
func extractBackdropZip(ctx context.Context, osUser, zipPath, installPath, stagingDir string) error {
	cmd := buildSystemdRunCmd(ctx, osUser, "unzip", "-q", "-o", zipPath, "-d", stagingDir)
	out, err := runBoundedOutput(cmd, 0)
	if err != nil {
		return fmt.Errorf("unzip backdrop: %w (output: %s)", err, truncateStr(string(out), 512))
	}
	src := filepath.Join(stagingDir, "backdrop")
	mvCmd := buildSystemdRunCmd(ctx, osUser, "sh", "-c",
		fmt.Sprintf("cp -a %s/. %s/ && rm -rf %s",
			shellQuote(src), shellQuote(installPath), shellQuote(src)),
	)
	mvOut, err := runBoundedOutput(mvCmd, 0)
	if err != nil {
		return fmt.Errorf("move backdrop contents: %w (output: %s)", err, truncateStr(string(mvOut), 512))
	}
	return nil
}

// extractBeeZip unzips the bee CLI tool zip into stagingDir. Returns
// the absolute path to bee.php inside the extracted tree.
//
// Layout differs by source: github auto-archive
// (`archive/refs/tags/...`) wraps everything under `bee-<version>/`,
// but the release-asset zip (`releases/download/<tag>/bee.zip`)
// places bee.php at the root. find handles both without us having to
// branch on URL format.
func extractBeeZip(ctx context.Context, osUser, zipPath, stagingDir string) (string, error) {
	cmd := buildSystemdRunCmd(ctx, osUser, "unzip", "-q", "-o", zipPath, "-d", stagingDir)
	if out, err := runBoundedOutput(cmd, 0); err != nil {
		return "", fmt.Errorf("unzip bee: %w (output: %s)", err, truncateStr(string(out), 512))
	}
	findCmd := buildSystemdRunCmd(ctx, osUser, "find", stagingDir, "-name", "bee.php", "-print", "-quit")
	out, err := runBoundedOutput(findCmd, 0)
	if err != nil {
		return "", fmt.Errorf("find bee.php: %w (output: %s)", err, truncateStr(string(out), 256))
	}
	beePath := strings.TrimSpace(string(out))
	if beePath == "" {
		return "", fmt.Errorf("bee.php not found after unzip in %s", stagingDir)
	}
	return beePath, nil
}

// runBeeSiteInstall drives bee's site-install command. bee needs to
// know the Backdrop install root via --root and reads its DB / admin
// settings from CLI flags.
//
// Flag set tracks bee 1.x-1.2.0:
//   - DB connection is split into --db-name / --db-user / --db-pass /
//     --db-host (the old --db-url flag was removed; passing it is silently
//     ignored and bee prompts for "Database name" interactively, hanging
//     forever even with --yes because the prompt is required-input)
//   - Admin account renamed --account-name → --username,
//     --account-pass → --password, --account-mail → --email
//   - --auto is required to suppress the interactive installer (--yes
//     alone only suppresses confirm-y/n prompts, not data prompts)
func runBeeSiteInstall(ctx context.Context, req backdropInstallReq, beeScript, installPath string) error {
	dbHost := req.DBHost
	if dbHost == "" {
		dbHost = "localhost"
	}
	profile := req.Profile
	if profile == "" {
		profile = "standard"
	}

	args := []string{
		"php", beeScript,
		"--root=" + installPath,
		"--yes",
		"site-install",
		"--auto",
		"--profile=" + profile,
		"--db-name=" + req.DBName,
		"--db-user=" + req.DBUser,
		"--db-pass=" + req.DBPassword,
		"--db-host=" + dbHost,
		"--username=" + req.AdminUser,
		"--password=" + req.AdminPass,
		"--email=" + req.AdminEmail,
		"--site-name=" + req.SiteTitle,
		"--site-mail=" + req.AdminEmail,
		"--langcode=en",
	}
	cmd := buildSystemdRunCmd(ctx, req.OSUser, args...)
	cmd.Dir = installPath
	out, err := runBoundedOutput(cmd, 0)
	if err != nil {
		return fmt.Errorf("bee site-install: %w (output: %s)", err, truncateStr(string(out), 1024))
	}
	return nil
}

func backdropInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req backdropInstallReq
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
	if req.SiteTitle == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "site_title is required"}
	}
	if req.AdminUser == "" || !backdropAdminUserPattern.MatchString(req.AdminUser) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_user must be 3-60 chars of letters, digits, dot, dash, or underscore",
		}
	}
	if req.AdminPass == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "admin_pass is required"}
	}
	if req.AdminEmail == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "admin_email is required"}
	}
	if req.Profile != "" && !backdropProfilePattern.MatchString(req.Profile) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("profile %q does not match install-profile machine-name form", req.Profile),
		}
	}
	if err := validateDocrootPath(req.OSUser, req.Docroot); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	installPath := computeBackdropInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		mkdirCmd := buildSystemdRunCmd(ctx, req.OSUser, "mkdir", "-p", installPath)
		if out, err := runBoundedOutput(mkdirCmd, 0); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mkdir %s: %v (output: %s)", installPath, err, truncateStr(string(out), 256))}
		}
	}

	removePlaceholderIndex(ctx, installPath)

	tmpDir, err := os.MkdirTemp("", "backdrop-")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mktemp: %v", err)}
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tmpdir: %v", err)}
	}
	backdropZipPath := filepath.Join(tmpDir, "backdrop.zip")
	beeZipPath := filepath.Join(tmpDir, "bee.zip")

	dlCtx, dlCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer dlCancel()
	if err := downloadBackdropZip(dlCtx, backdropZipPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := verifyBackdropSHA256(backdropZipPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := downloadBee(dlCtx, beeZipPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := verifyBeeSHA256(beeZipPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := os.Chmod(backdropZipPath, 0o644); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod backdrop zip: %v", err)}
	}
	if err := os.Chmod(beeZipPath, 0o644); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod bee zip: %v", err)}
	}

	stagingDir := filepath.Join(tmpDir, "stage")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mkdir staging: %v", err)}
	}
	if err := exec.CommandContext(ctx, "chown", "-R", req.OSUser+":"+req.OSUser, stagingDir).Run(); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chown staging: %v", err)}
	}

	if err := extractBackdropZip(ctx, req.OSUser, backdropZipPath, installPath, stagingDir); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	beeScript, err := extractBeeZip(ctx, req.OSUser, beeZipPath, stagingDir)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := runBeeSiteInstall(ctx, req, beeScript, installPath); err != nil {
		_ = exec.CommandContext(ctx, "rm", "-f", filepath.Join(installPath, "settings.php")).Run()
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := normalizePermsToWwwData(ctx, installPath, req.OSUser); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	return backdropInstallResp{Version: backdropVersion}, nil
}

func init() {
	RegisterAppInstaller("backdrop", backdropInstallHandler)
}
