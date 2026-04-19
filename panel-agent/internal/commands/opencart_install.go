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

type opencartInstallReq struct {
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
}

type opencartInstallResp struct {
	Version string `json:"version"`
}

// opencartVersion is the upstream OpenCart release this build targets.
// Bump alongside opencartZipSHA256 when moving to a new release.
//
// Releases: https://github.com/opencart/opencart/releases
// We pin a 4.x release because 4 is the current major and ships the
// composer-free release zip the cli_install.php expects.
const opencartVersion = "4.0.2.3"

var opencartZipURL = fmt.Sprintf(
	"https://github.com/opencart/opencart/releases/download/%s/opencart-%s.zip",
	opencartVersion, opencartVersion,
)

// opencartZipSHA256 is the SHA-256 of the zip at opencartZipURL as of
// the install-time pin. Empty value disables the check (DEV ONLY).
//
//	curl -sSL -A 'jabali-panel-agent/1.0 (+https://jabali.local)' \
//	  -L https://github.com/opencart/opencart/releases/download/4.0.2.3/opencart-4.0.2.3.zip \
//	  | sha256sum
const opencartZipSHA256 = ""

// opencartAdminUserPattern: OpenCart admin usernames are 3-20 chars,
// no special-character restriction beyond email-style chars.
var opencartAdminUserPattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{3,20}$`)

func computeOpenCartInstallPath(docroot, subdirectory string) string {
	if subdirectory == "" {
		return docroot
	}
	return filepath.Join(docroot, subdirectory)
}

func downloadOpenCartZip(ctx context.Context, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opencartZipURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", opencartZipURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", opencartZipURL, resp.StatusCode)
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

func verifyOpenCartSHA256(path string) error {
	if opencartZipSHA256 == "" {
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
	if !strings.EqualFold(got, opencartZipSHA256) {
		return fmt.Errorf("opencart zip sha256 mismatch: got %s want %s", got, opencartZipSHA256)
	}
	return nil
}

// extractOpenCartZip unzips into staging then flattens the upstream
// `upload/*` (NOT a versioned wrapper directory like the others) into
// installPath. OpenCart's release zip puts the actual webroot under
// `upload/` and ships sibling install/upgrade scripts at the zip
// root that we don't need.
//
// Also renames the two "*.dist" config samples to their final names —
// OpenCart's installer expects `config.php` and `admin/config.php` to
// be writable, so we create them empty (mode 0640) and let the
// installer write into them.
func extractOpenCartZip(ctx context.Context, osUser, zipPath, installPath, stagingDir string) error {
	cmd := buildSystemdRunCmd(ctx, osUser, "unzip", "-q", "-o", zipPath, "-d", stagingDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("unzip: %w (output: %s)", err, truncateStr(string(out), 512))
	}
	src := filepath.Join(stagingDir, "upload")
	mvCmd := buildSystemdRunCmd(ctx, osUser, "sh", "-c",
		fmt.Sprintf("cp -a %s/. %s/ && rm -rf %s",
			shellQuote(src), shellQuote(installPath), shellQuote(src)),
	)
	mvOut, err := mvCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("move opencart upload contents: %w (output: %s)", err, truncateStr(string(mvOut), 512))
	}

	// OpenCart ships config-dist.php samples that need to become
	// config.php in storefront and admin roots. Touch the files so
	// cli_install.php can write into them.
	for _, p := range []string{"config.php", filepath.Join("admin", "config.php")} {
		full := filepath.Join(installPath, p)
		touchCmd := buildSystemdRunCmd(ctx, osUser, "sh", "-c",
			fmt.Sprintf("touch %s && chmod 0640 %s", shellQuote(full), shellQuote(full)),
		)
		if tOut, err := touchCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("touch %s: %w (output: %s)", p, err, truncateStr(string(tOut), 256))
		}
	}
	return nil
}

// runOpenCartCLIInstaller drives `php install/cli_install.php install`.
// OpenCart's CLI installer materialises the schema, writes both
// config.php files, and creates the admin user in one shot.
func runOpenCartCLIInstaller(ctx context.Context, req opencartInstallReq, installPath string) error {
	dbHost := req.DBHost
	if dbHost == "" {
		dbHost = "localhost"
	}
	httpServer := req.SiteURL
	if !strings.HasSuffix(httpServer, "/") {
		httpServer += "/"
	}

	args := []string{
		"php", filepath.Join(installPath, "install", "cli_install.php"),
		"install",
		"--db_hostname", dbHost,
		"--db_username", req.DBUser,
		"--db_password", req.DBPassword,
		"--db_database", req.DBName,
		"--db_driver", "mysqli",
		"--db_port", "3306",
		"--db_prefix", "oc_",
		"--username", req.AdminUser,
		"--password", req.AdminPass,
		"--email", req.AdminEmail,
		"--http_server", httpServer,
	}
	cmd := buildSystemdRunCmd(ctx, req.OSUser, args...)
	cmd.Dir = installPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install/cli_install.php install: %w (output: %s)", err, truncateStr(string(out), 1024))
	}
	return nil
}

// removeOpenCartInstallDir deletes the `install/` directory after a
// successful install. OpenCart's runtime checks for its existence and
// surfaces a "delete the install folder" warning in admin if it's
// still around.
func removeOpenCartInstallDir(ctx context.Context, osUser, installPath string) error {
	cmd := buildSystemdRunCmd(ctx, osUser, "rm", "-rf", filepath.Join(installPath, "install"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rm install/: %w (output: %s)", err, truncateStr(string(out), 256))
	}
	return nil
}

func opencartInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req opencartInstallReq
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
	if req.AdminUser == "" || !opencartAdminUserPattern.MatchString(req.AdminUser) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_user must be 3-20 chars of letters, digits, dot, dash, underscore, or @",
		}
	}
	if len(req.AdminPass) < 4 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_pass must be at least 4 characters (OpenCart minimum)",
		}
	}
	if req.AdminEmail == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "admin_email is required"}
	}
	if err := validateDocrootPath(req.OSUser, req.Docroot); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	installPath := computeOpenCartInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		mkdirCmd := buildSystemdRunCmd(ctx, req.OSUser, "mkdir", "-p", installPath)
		if out, err := mkdirCmd.CombinedOutput(); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mkdir %s: %v (output: %s)", installPath, err, truncateStr(string(out), 256))}
		}
	}

	removePlaceholderIndex(ctx, installPath)

	tmpDir, err := os.MkdirTemp("", "opencart-")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mktemp: %v", err)}
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tmpdir: %v", err)}
	}
	zipPath := filepath.Join(tmpDir, "opencart.zip")

	dlCtx, dlCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer dlCancel()
	if err := downloadOpenCartZip(dlCtx, zipPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := verifyOpenCartSHA256(zipPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := os.Chmod(zipPath, 0o644); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod zip: %v", err)}
	}

	stagingDir := filepath.Join(tmpDir, "stage")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mkdir staging: %v", err)}
	}
	if err := exec.CommandContext(ctx, "chown", "-R", req.OSUser+":"+req.OSUser, stagingDir).Run(); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chown staging: %v", err)}
	}

	if err := extractOpenCartZip(ctx, req.OSUser, zipPath, installPath, stagingDir); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := runOpenCartCLIInstaller(ctx, req, installPath); err != nil {
		// Best-effort cleanup so re-install works.
		_ = exec.CommandContext(ctx, "rm", "-f",
			filepath.Join(installPath, "config.php"),
			filepath.Join(installPath, "admin", "config.php"),
		).Run()
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := removeOpenCartInstallDir(ctx, req.OSUser, installPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := normalizePermsToWwwData(ctx, installPath, req.OSUser); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	return opencartInstallResp{Version: opencartVersion}, nil
}

func init() {
	RegisterAppInstaller("opencart", opencartInstallHandler)
}
