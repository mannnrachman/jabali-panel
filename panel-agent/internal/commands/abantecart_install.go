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

type abantecartInstallReq struct {
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

type abantecartInstallResp struct {
	Version string `json:"version"`
}

// abantecartVersion is the upstream AbanteCart release this build
// targets. Bump alongside abantecartZipSHA256 when moving to a new
// release. The git tag is bare-numeric ("1.4.3") — no `v` prefix — so
// the archive URL below mirrors that. Hitting `v1.4.3.zip` returns 404.
//
// Releases: https://github.com/abantecart/abantecart-src/releases
const abantecartVersion = "1.4.3"

var abantecartZipURL = fmt.Sprintf(
	"https://github.com/abantecart/abantecart-src/archive/refs/tags/%s.zip",
	abantecartVersion,
)

const abantecartZipSHA256 = ""

var abantecartAdminUserPattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{3,30}$`)

func computeAbanteCartInstallPath(docroot, subdirectory string) string {
	if subdirectory == "" {
		return docroot
	}
	return filepath.Join(docroot, subdirectory)
}

func downloadAbanteCartZip(ctx context.Context, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, abantecartZipURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", abantecartZipURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", abantecartZipURL, resp.StatusCode)
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

func verifyAbanteCartSHA256(path string) error {
	if abantecartZipSHA256 == "" {
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
	if !strings.EqualFold(got, abantecartZipSHA256) {
		return fmt.Errorf("abantecart zip sha256 mismatch: got %s want %s", got, abantecartZipSHA256)
	}
	return nil
}

// extractAbanteCartZip unzips into staging then flattens
// abantecart-src-<version>/public_html/* into installPath. AbanteCart's
// github archive wraps the actual webroot under a `public_html/`
// directory inside the version-named folder.
func extractAbanteCartZip(ctx context.Context, osUser, zipPath, installPath, stagingDir string) error {
	cmd := buildSystemdRunCmd(ctx, osUser, "unzip", "-q", "-o", zipPath, "-d", stagingDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("unzip: %w (output: %s)", err, truncateStr(string(out), 512))
	}
	src := filepath.Join(stagingDir, "abantecart-src-"+abantecartVersion, "public_html")
	mvCmd := buildSystemdRunCmd(ctx, osUser, "sh", "-c",
		fmt.Sprintf("cp -a %s/. %s/ && rm -rf %s",
			shellQuote(src), shellQuote(installPath), shellQuote(stagingDir)),
	)
	mvOut, err := mvCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("move abantecart contents: %w (output: %s)", err, truncateStr(string(mvOut), 512))
	}
	return nil
}

// runAbanteCartCLIInstaller drives `php install/install.php` in CLI
// mode. The script reads INI-style flag pairs from argv (different
// from OpenCart's --flag value) and writes both system/config.php
// and admin/system/config.php in one shot.
func runAbanteCartCLIInstaller(ctx context.Context, req abantecartInstallReq, installPath string) error {
	dbHost := req.DBHost
	if dbHost == "" {
		dbHost = "localhost"
	}
	httpServer := req.SiteURL
	if !strings.HasSuffix(httpServer, "/") {
		httpServer += "/"
	}

	args := []string{
		"php",
		filepath.Join(installPath, "install", "abantecart_install.php"),
		"db_host=" + dbHost,
		"db_user=" + req.DBUser,
		"db_password=" + req.DBPassword,
		"db_name=" + req.DBName,
		"db_driver=mysqli",
		"db_port=3306",
		"db_prefix=ac_",
		"username=" + req.AdminUser,
		"password=" + req.AdminPass,
		"email=" + req.AdminEmail,
		"http_server=" + httpServer,
		"with_sample_data=0",
	}
	cmd := buildSystemdRunCmd(ctx, req.OSUser, args...)
	cmd.Dir = installPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install/abantecart_install.php: %w (output: %s)", err, truncateStr(string(out), 1024))
	}
	return nil
}

// removeAbanteCartInstallDir deletes the install/ directory after
// install. AbanteCart's runtime checks for it and surfaces a "remove
// the install folder" warning otherwise.
func removeAbanteCartInstallDir(ctx context.Context, osUser, installPath string) error {
	cmd := buildSystemdRunCmd(ctx, osUser, "rm", "-rf", filepath.Join(installPath, "install"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rm install/: %w (output: %s)", err, truncateStr(string(out), 256))
	}
	return nil
}

func abantecartInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req abantecartInstallReq
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
	if req.AdminUser == "" || !abantecartAdminUserPattern.MatchString(req.AdminUser) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_user must be 3-30 chars of letters, digits, dot, dash, underscore, or @",
		}
	}
	if len(req.AdminPass) < 7 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_pass must be at least 7 characters (AbanteCart minimum)",
		}
	}
	if req.AdminEmail == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "admin_email is required"}
	}
	if err := validateDocrootPath(req.OSUser, req.Docroot); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	installPath := computeAbanteCartInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		mkdirCmd := buildSystemdRunCmd(ctx, req.OSUser, "mkdir", "-p", installPath)
		if out, err := mkdirCmd.CombinedOutput(); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mkdir %s: %v (output: %s)", installPath, err, truncateStr(string(out), 256))}
		}
	}

	removePlaceholderIndex(ctx, installPath)

	tmpDir, err := os.MkdirTemp("", "abantecart-")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mktemp: %v", err)}
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tmpdir: %v", err)}
	}
	zipPath := filepath.Join(tmpDir, "abantecart.zip")

	dlCtx, dlCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer dlCancel()
	if err := downloadAbanteCartZip(dlCtx, zipPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := verifyAbanteCartSHA256(zipPath); err != nil {
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

	if err := extractAbanteCartZip(ctx, req.OSUser, zipPath, installPath, stagingDir); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := runAbanteCartCLIInstaller(ctx, req, installPath); err != nil {
		// Best-effort cleanup so re-install works.
		_ = exec.CommandContext(ctx, "rm", "-f",
			filepath.Join(installPath, "system", "config.php"),
			filepath.Join(installPath, "admin", "system", "config.php"),
		).Run()
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := removeAbanteCartInstallDir(ctx, req.OSUser, installPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := normalizePermsToWwwData(ctx, installPath, req.OSUser); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	return abantecartInstallResp{Version: abantecartVersion}, nil
}

func init() {
	RegisterAppInstaller("abantecart", abantecartInstallHandler)
}
