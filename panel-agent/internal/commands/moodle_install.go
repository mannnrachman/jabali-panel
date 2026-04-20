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

type moodleInstallReq struct {
	AppType       string `json:"app_type"`
	InstallID     string `json:"install_id"` // M19 framework: needed for managed-data-dir
	OSUser        string `json:"os_user"`
	Docroot       string `json:"docroot"`
	Subdirectory  string `json:"subdirectory"`
	SiteURL       string `json:"site_url"`
	UseWWW        bool   `json:"use_www"`
	DBName        string `json:"db_name"`
	DBUser        string `json:"db_user"`
	DBPassword    string `json:"db_password"`
	DBHost        string `json:"db_host"`
	SiteTitle     string `json:"site_title"`
	SiteShortName string `json:"site_short_name"`
	AdminUser     string `json:"admin_user"`
	AdminPass     string `json:"admin_pass"`
	AdminEmail    string `json:"admin_email"`
	Language      string `json:"language"`
}

type moodleInstallResp struct {
	Version string `json:"version"`
}

// moodleVersion is the upstream Moodle release this build targets.
// Bump alongside moodleTarballSHA256 when moving to a new release.
//
// Releases: https://download.moodle.org/releases/latest/
// Direct tarballs: https://download.moodle.org/download.php/direct/stable<MAJOR>/moodle-latest-<MAJOR>.tgz
// Pinned to a 4.4 build because 4.4 is the current LTS through 2026.
const moodleVersion = "4.4.2"

var moodleTarballURL = fmt.Sprintf(
	"https://download.moodle.org/download.php/direct/stable404/moodle-%s.tgz",
	moodleVersion,
)

const moodleTarballSHA256 = ""

// moodleAdminUserPattern: Moodle usernames must be lowercase by default
// (siteadminusername-policy may be loosened post-install). Allow
// alnum + dot/dash/underscore.
var moodleAdminUserPattern = regexp.MustCompile(`^[a-z0-9._-]{1,100}$`)

var moodleLanguagePattern = regexp.MustCompile(`^[a-z]{2,3}(_[a-z]{2,4})?$`)

func computeMoodleInstallPath(docroot, subdirectory string) string {
	if subdirectory == "" {
		return docroot
	}
	return filepath.Join(docroot, subdirectory)
}

func downloadMoodleTarball(ctx context.Context, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, moodleTarballURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", moodleTarballURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", moodleTarballURL, resp.StatusCode)
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

func verifyMoodleSHA256(path string) error {
	if moodleTarballSHA256 == "" {
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
	if !strings.EqualFold(got, moodleTarballSHA256) {
		return fmt.Errorf("moodle tarball sha256 mismatch: got %s want %s", got, moodleTarballSHA256)
	}
	return nil
}

// extractMoodleTarball untars moodle-X.Y.Z.tgz. The upstream tarball
// wraps content under a top-level "moodle/" directory; --strip-
// components=1 flattens it.
func extractMoodleTarball(ctx context.Context, osUser, tarballPath, installPath string) error {
	cmd := buildSystemdRunCmd(ctx, osUser,
		"tar",
		"--extract",
		"--gzip",
		"--strip-components=1",
		"--file", tarballPath,
		"--directory", installPath,
	)
	out, err := runBoundedOutput(cmd, 0)
	if err != nil {
		return fmt.Errorf("tar extract: %w (output: %s)", err, truncateStr(string(out), 512))
	}
	return nil
}

// runMoodleCLIInstaller drives admin/cli/install.php. Moodle's CLI
// installer materialises the schema, writes config.php, and creates
// the admin user in one shot.
//
// dataDir comes from the M19 managed-data-dir framework — pre-created
// outside the docroot so Moodle's "moodledata must not be web-served"
// guard is satisfied.
func runMoodleCLIInstaller(ctx context.Context, req moodleInstallReq, installPath, dataDir string) error {
	dbHost := req.DBHost
	if dbHost == "" {
		dbHost = "localhost"
	}
	lang := req.Language
	if lang == "" {
		lang = "en"
	}
	shortName := req.SiteShortName
	if shortName == "" {
		shortName = "Moodle"
	}

	args := []string{
		"php", filepath.Join(installPath, "admin", "cli", "install.php"),
		"--lang=" + lang,
		"--wwwroot=" + req.SiteURL,
		"--dataroot=" + dataDir,
		"--dbtype=mariadb",
		"--dbhost=" + dbHost,
		"--dbname=" + req.DBName,
		"--dbuser=" + req.DBUser,
		"--dbpass=" + req.DBPassword,
		"--prefix=mdl_",
		"--fullname=" + req.SiteTitle,
		"--shortname=" + shortName,
		"--adminuser=" + req.AdminUser,
		"--adminpass=" + req.AdminPass,
		"--adminemail=" + req.AdminEmail,
		"--agree-license",
		"--non-interactive",
	}
	cmd := buildSystemdRunCmd(ctx, req.OSUser, args...)
	cmd.Dir = installPath
	out, err := runBoundedOutput(cmd, 0)
	if err != nil {
		return fmt.Errorf("admin/cli/install.php: %w (output: %s)", err, truncateStr(string(out), 1024))
	}
	return nil
}

func moodleInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req moodleInstallReq
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("failed to parse params: %v", err)}
	}
	if req.OSUser == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "os_user is required"}
	}
	if req.InstallID == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "install_id is required (managed-data-dir framework needs it for moodledata path)"}
	}
	if req.Docroot == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "docroot is required"}
	}
	if req.SiteURL == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "site_url is required (Moodle bakes wwwroot into config.php)"}
	}
	if req.DBName == "" || req.DBUser == "" || req.DBPassword == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "db_name, db_user, db_password are required"}
	}
	if req.SiteTitle == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "site_title is required"}
	}
	if req.AdminUser == "" || !moodleAdminUserPattern.MatchString(req.AdminUser) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_user must be 1-100 chars of lowercase letters, digits, dot, dash, or underscore",
		}
	}
	if len(req.AdminPass) < 8 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_pass must be at least 8 characters (Moodle minimum)",
		}
	}
	if req.AdminEmail == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "admin_email is required"}
	}
	if req.Language != "" && !moodleLanguagePattern.MatchString(req.Language) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("language %q does not match expected lang-pack code form", req.Language),
		}
	}
	if err := validateDocrootPath(req.OSUser, req.Docroot); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	installPath := computeMoodleInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		mkdirCmd := buildSystemdRunCmd(ctx, req.OSUser, "mkdir", "-p", installPath)
		if out, err := runBoundedOutput(mkdirCmd, 0); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mkdir %s: %v (output: %s)", installPath, err, truncateStr(string(out), 256))}
		}
	}

	removePlaceholderIndex(ctx, installPath)

	// Stand up moodledata via the M19 managed-data-dir framework. The
	// path is deterministic from (osUser, installID); the deleter
	// recomputes the same path to clean up.
	dataDir, err := ensureManagedDataDir(ctx, req.OSUser, req.InstallID)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("ensure moodledata: %v", err)}
	}

	tmpDir, err := os.MkdirTemp("", "moodle-")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mktemp: %v", err)}
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tmpdir: %v", err)}
	}
	tarballPath := filepath.Join(tmpDir, "moodle.tgz")

	dlCtx, dlCancel := context.WithTimeout(ctx, 15*time.Minute)
	defer dlCancel()
	if err := downloadMoodleTarball(dlCtx, tarballPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := verifyMoodleSHA256(tarballPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := os.Chmod(tarballPath, 0o644); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tarball: %v", err)}
	}

	if err := extractMoodleTarball(ctx, req.OSUser, tarballPath, installPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	// Moodle's install.php is the long pole — schema migrations on a
	// fresh DB take 5-10 minutes. 20-min budget keeps headroom.
	installCtx, installCancel := context.WithTimeout(ctx, 20*time.Minute)
	defer installCancel()
	if err := runMoodleCLIInstaller(installCtx, req, installPath, dataDir); err != nil {
		// Best-effort cleanup: rm config.php so re-install isn't blocked.
		_ = exec.CommandContext(ctx, "rm", "-f", filepath.Join(installPath, "config.php")).Run()
		// Leave moodledata in place — it may have partial state useful
		// for diagnostics; the deleter will clean it on a subsequent
		// app delete.
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := normalizePermsToWwwData(ctx, installPath, req.OSUser); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	return moodleInstallResp{Version: moodleVersion}, nil
}

func init() {
	RegisterAppInstaller("moodle", moodleInstallHandler)
}
