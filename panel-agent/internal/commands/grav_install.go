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

// gravInstallReq is the input for the Grav installer. RequiresDB=false
// so no DB fields, mirroring the DokuWiki shape.
type gravInstallReq struct {
	AppType       string `json:"app_type"`
	OSUser        string `json:"os_user"`
	Docroot       string `json:"docroot"`
	Subdirectory  string `json:"subdirectory"`
	SiteURL       string `json:"site_url"`
	UseWWW        bool   `json:"use_www"`
	SiteTitle     string `json:"site_title"`
	AdminUser     string `json:"admin_user"`
	AdminPass     string `json:"admin_pass"`
	AdminEmail    string `json:"admin_email"`
	AdminFullName string `json:"admin_full_name"`
}

type gravInstallResp struct {
	Version string `json:"version"`
}

// gravVersion is the upstream Grav release this build targets. Bump
// alongside gravZipSHA256 when moving to a new release.
//
// Releases: https://github.com/getgrav/grav/releases
// We pull the `grav-admin` distribution which bundles core + the admin
// plugin so the user has a working /admin login URL on first visit.
const gravVersion = "1.7.45"

var gravZipURL = fmt.Sprintf(
	"https://github.com/getgrav/grav/releases/download/%s/grav-admin-v%s.zip",
	gravVersion, gravVersion,
)

// gravZipSHA256 is the SHA-256 of the zip at gravZipURL as of the
// install-time pin. Empty value disables the integrity check
// (DEV ONLY — production builds MUST set this).
//
//	curl -sSL -A 'jabali-panel-agent/1.0 (+https://jabali.local)' \
//	  https://github.com/getgrav/grav/releases/download/1.7.45/grav-admin-v1.7.45.zip \
//	  | sha256sum
const gravZipSHA256 = ""

// gravAdminUserPattern: Grav usernames must be 3+ chars, alphanumeric.
// Allow dot/dash/underscore for slightly more flexibility; Grav itself
// is permissive.
var gravAdminUserPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,40}$`)

func computeGravInstallPath(docroot, subdirectory string) string {
	if subdirectory == "" {
		return docroot
	}
	return filepath.Join(docroot, subdirectory)
}

func downloadGravZip(ctx context.Context, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gravZipURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{
		Timeout: 10 * time.Minute,
		// Github releases redirect to objects.githubusercontent.com; let
		// the default policy follow up to 10 hops, which it does.
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", gravZipURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", gravZipURL, resp.StatusCode)
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

func verifyGravSHA256(path string) error {
	if gravZipSHA256 == "" {
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
	if !strings.EqualFold(got, gravZipSHA256) {
		return fmt.Errorf("grav zip sha256 mismatch: got %s want %s", got, gravZipSHA256)
	}
	return nil
}

// extractGravZip unzips the Grav distribution. The upstream zip wraps
// content under a top-level `grav-admin/` directory; we extract to a
// staging dir then move the inner contents into installPath. unzip
// has no equivalent to tar's --strip-components.
func extractGravZip(ctx context.Context, osUser, zipPath, installPath, stagingDir string) error {
	// 1. unzip into stagingDir
	cmd := buildSystemdRunCmd(ctx, osUser, "unzip", "-q", "-o", zipPath, "-d", stagingDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("unzip: %w (output: %s)", err, truncateStr(string(out), 512))
	}

	// 2. move stagingDir/grav-admin/* into installPath. Use shell
	// expansion via sh -c to handle the dotfiles (.htaccess, .htrouter.php).
	src := filepath.Join(stagingDir, "grav-admin")
	mvCmd := buildSystemdRunCmd(ctx, osUser, "sh", "-c",
		fmt.Sprintf("cp -a %s/. %s/ && rm -rf %s",
			shellQuote(src), shellQuote(installPath), shellQuote(src)),
	)
	mvOut, err := mvCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("move grav-admin contents: %w (output: %s)", err, truncateStr(string(mvOut), 512))
	}
	return nil
}

// shellQuote single-quotes a path for embedding in `sh -c "…"`. Keeps
// the trust boundary tight: paths come from the agent's own filesystem
// and the install user, not from arbitrary user input, but defensive
// quoting is still worth the 4 lines.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// runGravNewUser creates the admin user via Grav's login plugin CLI.
// Grav doesn't have a "create admin during install" wizard the way
// WordPress does — admin user creation is a post-extract step.
//
// Flag reference: https://github.com/getgrav/grav-plugin-login
//   -u username   -p password   -e email   -P access-level (b = admin.super)
//   -N full-name  -t title
func runGravNewUser(ctx context.Context, req gravInstallReq, installPath string) error {
	fullName := req.AdminFullName
	if fullName == "" {
		fullName = "Site Administrator"
	}

	args := []string{
		filepath.Join(installPath, "bin", "plugin"),
		"login",
		"newuser",
		"--user=" + req.AdminUser,
		"--password=" + req.AdminPass,
		"--email=" + req.AdminEmail,
		"--permissions=b", // admin.super
		"--fullname=" + fullName,
		"--title=Administrator",
	}
	cmd := buildSystemdRunCmd(ctx, req.OSUser, args...)
	cmd.Dir = installPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bin/plugin login newuser: %w (output: %s)", err, truncateStr(string(out), 1024))
	}
	return nil
}

// writeGravSiteConfig sets the public site title in user/config/site.yaml.
// Grav reads this at runtime, not at install — so the file just needs
// to land before first request. We write a minimal YAML here rather
// than calling another CLI command for one field.
func writeGravSiteConfig(ctx context.Context, osUser, installPath, siteTitle string) error {
	configDir := filepath.Join(installPath, "user", "config")
	mkCmd := buildSystemdRunCmd(ctx, osUser, "mkdir", "-p", configDir)
	if out, err := mkCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mkdir user/config: %w (output: %s)", err, truncateStr(string(out), 256))
	}

	yaml := "title: " + yamlSingleQuoted(siteTitle) + "\n"
	configPath := filepath.Join(configDir, "site.yaml")
	teeCmd := buildSystemdRunCmd(ctx, osUser, "sh", "-c",
		fmt.Sprintf("cat > %s", shellQuote(configPath)),
	)
	teeCmd.Stdin = strings.NewReader(yaml)
	if out, err := teeCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("write user/config/site.yaml: %w (output: %s)", err, truncateStr(string(out), 256))
	}
	return nil
}

func gravInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req gravInstallReq
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("failed to parse params: %v", err)}
	}
	if req.OSUser == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "os_user is required"}
	}
	if req.Docroot == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "docroot is required"}
	}
	if req.SiteTitle == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "site_title is required"}
	}
	if req.AdminUser == "" || !gravAdminUserPattern.MatchString(req.AdminUser) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_user must be 3-40 chars of letters, digits, dot, dash, or underscore",
		}
	}
	if len(req.AdminPass) < 8 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_pass must be at least 8 characters (Grav minimum)",
		}
	}
	if req.AdminEmail == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "admin_email is required"}
	}
	if err := validateDocrootPath(req.OSUser, req.Docroot); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	installPath := computeGravInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		mkdirCmd := buildSystemdRunCmd(ctx, req.OSUser, "mkdir", "-p", installPath)
		if out, err := mkdirCmd.CombinedOutput(); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("mkdir %s: %v (output: %s)", installPath, err, truncateStr(string(out), 256)),
			}
		}
	}

	removePlaceholderIndex(ctx, installPath)

	tmpDir, err := os.MkdirTemp("", "grav-")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mktemp: %v", err)}
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tmpdir: %v", err)}
	}
	zipPath := filepath.Join(tmpDir, "grav-admin.zip")

	dlCtx, dlCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer dlCancel()
	if err := downloadGravZip(dlCtx, zipPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := verifyGravSHA256(zipPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := os.Chmod(zipPath, 0o644); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod zip: %v", err)}
	}

	// Stage the unzip in a sibling dir so we can flatten grav-admin/*
	// into installPath without a separate strip-components step.
	stagingDir := filepath.Join(tmpDir, "stage")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mkdir staging: %v", err)}
	}
	if err := exec.CommandContext(ctx, "chown", "-R", req.OSUser+":"+req.OSUser, stagingDir).Run(); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chown staging: %v", err)}
	}

	if err := extractGravZip(ctx, req.OSUser, zipPath, installPath, stagingDir); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := writeGravSiteConfig(ctx, req.OSUser, installPath, req.SiteTitle); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := runGravNewUser(ctx, req, installPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := normalizePermsToWwwData(ctx, installPath, req.OSUser); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	return gravInstallResp{Version: gravVersion}, nil
}

func init() {
	RegisterAppInstaller("grav", gravInstallHandler)
}
