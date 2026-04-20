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

// freshrssInstallReq is the input for the FreshRSS installer.
type freshrssInstallReq struct {
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

type freshrssInstallResp struct {
	Version string `json:"version"`
}

// freshrssVersion is the upstream FreshRSS release this build targets.
// Bump alongside freshrssTarballSHA256 when moving to a new release.
//
// Releases: https://github.com/FreshRSS/FreshRSS/releases
// FreshRSS publishes .tar.gz and .zip; we use .tar.gz to share the
// extract-with-tar code path used by the other apps.
const freshrssVersion = "1.24.1"

var freshrssTarballURL = fmt.Sprintf(
	"https://github.com/FreshRSS/FreshRSS/archive/refs/tags/%s.tar.gz",
	freshrssVersion,
)

// freshrssTarballSHA256 is the SHA-256 of the tarball at
// freshrssTarballURL as of the install-time pin. Empty value disables
// the integrity check (DEV ONLY — production builds MUST set this).
//
//	curl -sSL -A 'jabali-panel-agent/1.0 (+https://jabali.local)' \
//	  https://github.com/FreshRSS/FreshRSS/archive/refs/tags/1.24.1.tar.gz \
//	  | sha256sum
const freshrssTarballSHA256 = ""

// freshrssAdminUserPattern: FreshRSS allows alnum + underscore, 1-16
// chars by default (configurable). We allow up to 32 to be slightly
// more flexible.
var freshrssAdminUserPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

// freshrssLanguagePattern matches the lang-pack codes FreshRSS uses
// (en, fr, de, zh-cn, ...).
var freshrssLanguagePattern = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{1,8})?$`)

func computeFreshRSSInstallPath(docroot, subdirectory string) string {
	if subdirectory == "" {
		return docroot
	}
	return filepath.Join(docroot, subdirectory)
}

func downloadFreshRSSTarball(ctx context.Context, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, freshrssTarballURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", freshrssTarballURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", freshrssTarballURL, resp.StatusCode)
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

func verifyFreshRSSSHA256(path string) error {
	if freshrssTarballSHA256 == "" {
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
	if !strings.EqualFold(got, freshrssTarballSHA256) {
		return fmt.Errorf("freshrss tarball sha256 mismatch: got %s want %s", got, freshrssTarballSHA256)
	}
	return nil
}

// extractFreshRSSTarball untars the GitHub release archive. The
// archive wraps content under FreshRSS-<version>/ so --strip-
// components=1 flattens it into installPath.
func extractFreshRSSTarball(ctx context.Context, osUser, tarballPath, installPath string) error {
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

// runFreshRSSInstall drives cli/do-install.php to write the data
// store + DB schema. FreshRSS's installer reads its config from CLI
// flags; same /proc/cmdline exposure as the other CLI installers.
func runFreshRSSInstall(ctx context.Context, req freshrssInstallReq, installPath string) error {
	dbHost := req.DBHost
	if dbHost == "" {
		dbHost = "localhost"
	}
	lang := req.Language
	if lang == "" {
		lang = "en"
	}

	args := []string{
		"php", filepath.Join(installPath, "cli", "do-install.php"),
		"--default_user=" + req.AdminUser,
		"--auth_type=form",
		"--language=" + lang,
		"--db-type=mysql",
		"--db-host=" + dbHost,
		"--db-base=" + req.DBName,
		"--db-user=" + req.DBUser,
		"--db-password=" + req.DBPassword,
		"--db-prefix=freshrss_",
	}
	cmd := buildSystemdRunCmd(ctx, req.OSUser, args...)
	cmd.Dir = installPath
	out, err := runBoundedOutput(cmd, 0)
	if err != nil {
		return fmt.Errorf("cli/do-install.php: %w (output: %s)", err, truncateStr(string(out), 1024))
	}
	return nil
}

// runFreshRSSCreateUser drives cli/create-user.php to create the
// admin account. FreshRSS's `do-install` only creates the data store
// and writes config; user creation is a separate step.
func runFreshRSSCreateUser(ctx context.Context, req freshrssInstallReq, installPath string) error {
	lang := req.Language
	if lang == "" {
		lang = "en"
	}
	args := []string{
		"php", filepath.Join(installPath, "cli", "create-user.php"),
		"--user=" + req.AdminUser,
		"--password=" + req.AdminPass,
		"--email=" + req.AdminEmail,
		"--language=" + lang,
	}
	cmd := buildSystemdRunCmd(ctx, req.OSUser, args...)
	cmd.Dir = installPath
	out, err := runBoundedOutput(cmd, 0)
	if err != nil {
		return fmt.Errorf("cli/create-user.php: %w (output: %s)", err, truncateStr(string(out), 1024))
	}
	return nil
}

func freshrssInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req freshrssInstallReq
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
	if req.AdminUser == "" || !freshrssAdminUserPattern.MatchString(req.AdminUser) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_user must be 1-32 chars of letters, digits, or underscore",
		}
	}
	if len(req.AdminPass) < 7 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_pass must be at least 7 characters (FreshRSS minimum)",
		}
	}
	if req.AdminEmail == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "admin_email is required"}
	}
	if req.Language != "" && !freshrssLanguagePattern.MatchString(req.Language) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("language %q does not match expected ISO-639 form", req.Language),
		}
	}
	if err := validateDocrootPath(req.OSUser, req.Docroot); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	installPath := computeFreshRSSInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		mkdirCmd := buildSystemdRunCmd(ctx, req.OSUser, "mkdir", "-p", installPath)
		if out, err := runBoundedOutput(mkdirCmd, 0); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("mkdir %s: %v (output: %s)", installPath, err, truncateStr(string(out), 256)),
			}
		}
	}

	removePlaceholderIndex(ctx, installPath)

	tmpDir, err := os.MkdirTemp("", "freshrss-")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mktemp: %v", err)}
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tmpdir: %v", err)}
	}
	tarballPath := filepath.Join(tmpDir, "freshrss.tar.gz")

	dlCtx, dlCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer dlCancel()
	if err := downloadFreshRSSTarball(dlCtx, tarballPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := verifyFreshRSSSHA256(tarballPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := os.Chmod(tarballPath, 0o644); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tarball: %v", err)}
	}
	if err := extractFreshRSSTarball(ctx, req.OSUser, tarballPath, installPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := runFreshRSSInstall(ctx, req, installPath); err != nil {
		// Best-effort cleanup of the half-written data store so re-install
		// isn't blocked.
		_ = exec.CommandContext(ctx, "rm", "-rf", filepath.Join(installPath, "data", "config.php")).Run()
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := runFreshRSSCreateUser(ctx, req, installPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := normalizePermsToWwwData(ctx, installPath, req.OSUser); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	return freshrssInstallResp{Version: freshrssVersion}, nil
}

func init() {
	RegisterAppInstaller("freshrss", freshrssInstallHandler)
}
