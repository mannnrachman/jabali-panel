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

type concreteInstallReq struct {
	AppType       string `json:"app_type"`
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
	AdminUser     string `json:"admin_user"`
	AdminPass     string `json:"admin_pass"`
	AdminEmail    string `json:"admin_email"`
	StartingPoint string `json:"starting_point"`
	Locale        string `json:"locale"`
}

type concreteInstallResp struct {
	Version string `json:"version"`
}

// concreteVersion is the upstream Concrete CMS release this build
// targets. Bump alongside concreteZipSHA256 when moving to a new
// release. Asset filename is `concrete-cms-<version>.zip` (with
// hyphen) — early-9.x used `concretecms-` (no hyphen) but every
// 9.4.x and 9.5.x release ships hyphenated. Using the wrong form
// returns 404.
//
// Releases: https://github.com/concretecms/concretecms/releases
const concreteVersion = "9.4.8"

var concreteZipURL = fmt.Sprintf(
	"https://github.com/concretecms/concretecms/releases/download/%s/concrete-cms-%s.zip",
	concreteVersion, concreteVersion,
)

const concreteZipSHA256 = ""

var concreteAdminUserPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,60}$`)

var concreteStartingPointPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,49}$`)

var concreteLocalePattern = regexp.MustCompile(`^[a-z]{2,3}(_[A-Z]{2})?$`)

func computeConcreteInstallPath(docroot, subdirectory string) string {
	if subdirectory == "" {
		return docroot
	}
	return filepath.Join(docroot, subdirectory)
}

func downloadConcreteZip(ctx context.Context, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, concreteZipURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", concreteZipURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", concreteZipURL, resp.StatusCode)
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

func verifyConcreteSHA256(path string) error {
	if concreteZipSHA256 == "" {
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
	if !strings.EqualFold(got, concreteZipSHA256) {
		return fmt.Errorf("concrete zip sha256 mismatch: got %s want %s", got, concreteZipSHA256)
	}
	return nil
}

// extractConcreteZip unzips into staging then flattens the wrapper
// dir into installPath. The wrapper is `concrete-cms-<version>/` for
// 9.4.x+ and `concretecms-<version>/` for early-9.x — probe both
// rather than hard-code so a release-naming flip doesn't break us
// silently. Falls back to "first directory in staging" if neither is
// present, since concrete keeps changing the slug shape.
func extractConcreteZip(ctx context.Context, osUser, zipPath, installPath, stagingDir string) error {
	cmd := buildSystemdRunCmd(ctx, osUser, "unzip", "-q", "-o", zipPath, "-d", stagingDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("unzip: %w (output: %s)", err, truncateStr(string(out), 512))
	}
	src, srcErr := findConcreteWrapper(ctx, osUser, stagingDir)
	if srcErr != nil {
		return fmt.Errorf("locate concrete wrapper: %w", srcErr)
	}
	// stagingDir cleanup deferred to caller's os.RemoveAll(tmpDir) which
	// runs as root — the per-domain user can't `rm` an entry from
	// stagingDir's parent (mode 0755 root-owned).
	mvCmd := buildSystemdRunCmd(ctx, osUser, "sh", "-c",
		fmt.Sprintf("cp -a %s/. %s/",
			shellQuote(src), shellQuote(installPath)),
	)
	mvOut, err := mvCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("move concrete contents: %w (output: %s)", err, truncateStr(string(mvOut), 512))
	}
	return nil
}

// findConcreteWrapper picks the wrapper directory the unzip produced.
// Looks for the two known-shape names first (`concrete-cms-<v>` and
// `concretecms-<v>`); falls back to the first directory under
// stagingDir if neither matches. The find runs as the per-domain user
// since the unzip ran the same way and stagingDir is user-owned.
func findConcreteWrapper(ctx context.Context, osUser, stagingDir string) (string, error) {
	for _, name := range []string{
		"concrete-cms-" + concreteVersion,
		"concretecms-" + concreteVersion,
	} {
		candidate := filepath.Join(stagingDir, name)
		statCmd := buildSystemdRunCmd(ctx, osUser, "test", "-d", candidate)
		if err := statCmd.Run(); err == nil {
			return candidate, nil
		}
	}
	// Fallback: pick the first directory entry under stagingDir.
	listCmd := buildSystemdRunCmd(ctx, osUser, "sh", "-c",
		fmt.Sprintf("find %s -mindepth 1 -maxdepth 1 -type d | head -n1",
			shellQuote(stagingDir)))
	out, err := listCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("list staging: %w", err)
	}
	first := strings.TrimSpace(string(out))
	if first == "" {
		return "", fmt.Errorf("no directory found in %s", stagingDir)
	}
	return first, nil
}

// runConcreteCLIInstaller drives `concrete/bin/concrete c5:install`.
// Concrete handles DB schema, admin user creation, starting-point
// import, and config write in one shot.
func runConcreteCLIInstaller(ctx context.Context, req concreteInstallReq, installPath string) error {
	dbHost := req.DBHost
	if dbHost == "" {
		dbHost = "localhost"
	}
	startingPoint := req.StartingPoint
	if startingPoint == "" {
		startingPoint = "elemental_full"
	}
	locale := req.Locale
	if locale == "" {
		locale = "en_US"
	}

	args := []string{
		"php",
		filepath.Join(installPath, "concrete", "bin", "concrete"),
		"c5:install",
		"--db-server=" + dbHost,
		"--db-username=" + req.DBUser,
		"--db-password=" + req.DBPassword,
		"--db-database=" + req.DBName,
		"--site=" + req.SiteTitle,
		"--starting-point=" + startingPoint,
		"--admin-email=" + req.AdminEmail,
		"--admin-password=" + req.AdminPass,
		"--site-locale=" + locale,
		"--no-interaction",
	}
	cmd := buildSystemdRunCmd(ctx, req.OSUser, args...)
	cmd.Dir = installPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("concrete c5:install: %w (output: %s)", err, truncateStr(string(out), 1024))
	}
	return nil
}

func concreteInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req concreteInstallReq
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
	if req.AdminUser == "" || !concreteAdminUserPattern.MatchString(req.AdminUser) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_user must be 3-60 chars of letters, digits, dot, dash, or underscore",
		}
	}
	if len(req.AdminPass) < 5 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_pass must be at least 5 characters (Concrete CMS minimum)",
		}
	}
	if req.AdminEmail == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "admin_email is required"}
	}
	if req.StartingPoint != "" && !concreteStartingPointPattern.MatchString(req.StartingPoint) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("starting_point %q does not match expected machine-name form", req.StartingPoint),
		}
	}
	if req.Locale != "" && !concreteLocalePattern.MatchString(req.Locale) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("locale %q must be e.g. en_US", req.Locale),
		}
	}
	if err := validateDocrootPath(req.OSUser, req.Docroot); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	installPath := computeConcreteInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		mkdirCmd := buildSystemdRunCmd(ctx, req.OSUser, "mkdir", "-p", installPath)
		if out, err := mkdirCmd.CombinedOutput(); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mkdir %s: %v (output: %s)", installPath, err, truncateStr(string(out), 256))}
		}
	}

	removePlaceholderIndex(ctx, installPath)

	tmpDir, err := os.MkdirTemp("", "concrete-")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mktemp: %v", err)}
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tmpdir: %v", err)}
	}
	zipPath := filepath.Join(tmpDir, "concrete.zip")

	dlCtx, dlCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer dlCancel()
	if err := downloadConcreteZip(dlCtx, zipPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := verifyConcreteSHA256(zipPath); err != nil {
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

	if err := extractConcreteZip(ctx, req.OSUser, zipPath, installPath, stagingDir); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := runConcreteCLIInstaller(ctx, req, installPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := normalizePermsToWwwData(ctx, installPath, req.OSUser); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	return concreteInstallResp{Version: concreteVersion}, nil
}

func init() {
	RegisterAppInstaller("concrete", concreteInstallHandler)
}
