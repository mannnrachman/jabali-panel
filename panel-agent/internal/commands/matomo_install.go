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

type matomoInstallReq struct {
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

type matomoInstallResp struct {
	Version string `json:"version"`
}

// matomoVersion is the upstream Matomo release this build targets.
// Bump alongside matomoZipSHA256 when moving to a new release.
//
// Releases: https://builds.matomo.org/
// Matomo publishes .zip and .tar.gz; we use .zip for parity with Grav
// (only one app-type using zip means we already require unzip).
const matomoVersion = "5.2.0"

var matomoZipURL = fmt.Sprintf(
	"https://builds.matomo.org/matomo-%s.zip",
	matomoVersion,
)

// matomoZipSHA256 is the SHA-256 of the zip at matomoZipURL as of the
// install-time pin. Empty value disables the check (DEV ONLY).
//
//	curl -sSL -A 'jabali-panel-agent/1.0 (+https://jabali.local)' \
//	  https://builds.matomo.org/matomo-5.2.0.zip | sha256sum
const matomoZipSHA256 = ""

var matomoAdminUserPattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]{3,100}$`)

func computeMatomoInstallPath(docroot, subdirectory string) string {
	if subdirectory == "" {
		return docroot
	}
	return filepath.Join(docroot, subdirectory)
}

func downloadMatomoZip(ctx context.Context, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, matomoZipURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", matomoZipURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", matomoZipURL, resp.StatusCode)
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

func verifyMatomoSHA256(path string) error {
	if matomoZipSHA256 == "" {
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
	if !strings.EqualFold(got, matomoZipSHA256) {
		return fmt.Errorf("matomo zip sha256 mismatch: got %s want %s", got, matomoZipSHA256)
	}
	return nil
}

// extractMatomoZip unzips into a staging dir then flattens matomo/* into
// installPath. Same approach as Grav since Matomo's zip also has a
// top-level "matomo/" wrapper directory.
func extractMatomoZip(ctx context.Context, osUser, zipPath, installPath, stagingDir string) error {
	cmd := buildSystemdRunCmd(ctx, osUser, "unzip", "-q", "-o", zipPath, "-d", stagingDir)
	out, err := runBoundedOutput(cmd, 0)
	if err != nil {
		return fmt.Errorf("unzip: %w (output: %s)", err, truncateStr(string(out), 512))
	}
	src := filepath.Join(stagingDir, "matomo")
	mvCmd := buildSystemdRunCmd(ctx, osUser, "sh", "-c",
		fmt.Sprintf("cp -a %s/. %s/ && rm -rf %s",
			shellQuote(src), shellQuote(installPath), shellQuote(src)),
	)
	mvOut, err := runBoundedOutput(mvCmd, 0)
	if err != nil {
		return fmt.Errorf("move matomo contents: %w (output: %s)", err, truncateStr(string(mvOut), 512))
	}
	return nil
}

// writeMatomoConfig writes config/config.ini.php with the DB connection
// and a few sane defaults. Matomo's installer reads this on startup
// and refuses to run the install wizard if the file already declares
// a working DB — so we MUST omit the [General] section's
// installation_in_progress flag (or set it to 1) so console
// core:install knows to bootstrap.
func writeMatomoConfig(ctx context.Context, osUser, installPath string, req matomoInstallReq) error {
	dbHost := req.DBHost
	if dbHost == "" {
		dbHost = "localhost"
	}

	configDir := filepath.Join(installPath, "config")
	mkCmd := buildSystemdRunCmd(ctx, osUser, "mkdir", "-p", configDir)
	if out, err := runBoundedOutput(mkCmd, 0); err != nil {
		return fmt.Errorf("mkdir config: %w (output: %s)", err, truncateStr(string(out), 256))
	}

	// INI escape: backslashes and double-quotes in values must be escaped
	// inside double-quoted INI strings.
	esc := func(s string) string {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return `"` + s + `"`
	}

	ini := "; <?php exit; ?> DO NOT REMOVE THIS LINE\n" +
		"[database]\n" +
		"host = " + esc(dbHost) + "\n" +
		"username = " + esc(req.DBUser) + "\n" +
		"password = " + esc(req.DBPassword) + "\n" +
		"dbname = " + esc(req.DBName) + "\n" +
		"tables_prefix = \"matomo_\"\n" +
		"adapter = \"PDO\\MYSQL\"\n" +
		"\n" +
		"[General]\n" +
		"salt = " + esc(generateMatomoSalt()) + "\n" +
		"installation_in_progress = 1\n"

	configPath := filepath.Join(configDir, "config.ini.php")
	teeCmd := buildSystemdRunCmd(ctx, osUser, "sh", "-c",
		fmt.Sprintf("cat > %s", shellQuote(configPath)),
	)
	teeCmd.Stdin = strings.NewReader(ini)
	if out, err := runBoundedOutput(teeCmd, 0); err != nil {
		return fmt.Errorf("write config.ini.php: %w (output: %s)", err, truncateStr(string(out), 256))
	}
	if err := exec.CommandContext(ctx, "chmod", "0640", configPath).Run(); err != nil {
		return fmt.Errorf("chmod config.ini.php: %w", err)
	}
	return nil
}

// generateMatomoSalt generates a 64-char hex salt for the Matomo
// [General] section. Matomo uses this to sign cookies + various
// internal hashes; rotating it invalidates all logged-in sessions but
// is otherwise safe.
func generateMatomoSalt() string {
	b := make([]byte, 32)
	if _, err := os.ReadFile("/dev/urandom"); err == nil {
		// Read 32 bytes from urandom directly since crypto/rand is
		// already used elsewhere; this avoids importing it just for
		// one short call.
		f, err := os.Open("/dev/urandom")
		if err == nil {
			defer f.Close()
			if _, err := f.Read(b); err == nil {
				return hex.EncodeToString(b)
			}
		}
	}
	// Fallback to a deterministic-ish value from current time + pid.
	// This path only runs if /dev/urandom is broken, which means the
	// host has bigger problems than a weak salt.
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

// runMatomoCoreInstall runs `console core:install` to materialise the
// DB schema. The console reads config.ini.php for connection info.
func runMatomoCoreInstall(ctx context.Context, osUser, installPath string) error {
	args := []string{
		"php",
		filepath.Join(installPath, "console"),
		"core:install",
		"-n",
	}
	cmd := buildSystemdRunCmd(ctx, osUser, args...)
	cmd.Dir = installPath
	out, err := runBoundedOutput(cmd, 0)
	if err != nil {
		return fmt.Errorf("console core:install: %w (output: %s)", err, truncateStr(string(out), 1024))
	}
	return nil
}

// runMatomoUserCreate provisions the admin via console user:create.
func runMatomoUserCreate(ctx context.Context, req matomoInstallReq, installPath string) error {
	args := []string{
		"php",
		filepath.Join(installPath, "console"),
		"user:create",
		"--login=" + req.AdminUser,
		"--password=" + req.AdminPass,
		"--email=" + req.AdminEmail,
		"--user-superuser",
	}
	cmd := buildSystemdRunCmd(ctx, req.OSUser, args...)
	cmd.Dir = installPath
	out, err := runBoundedOutput(cmd, 0)
	if err != nil {
		return fmt.Errorf("console user:create: %w (output: %s)", err, truncateStr(string(out), 1024))
	}
	return nil
}

// finalizeMatomoConfig flips installation_in_progress off so Matomo
// stops bouncing requests to the install wizard.
func finalizeMatomoConfig(ctx context.Context, osUser, installPath string) error {
	configPath := filepath.Join(installPath, "config", "config.ini.php")
	cmd := buildSystemdRunCmd(ctx, osUser, "sed", "-i",
		"s/^installation_in_progress = 1$/installation_in_progress = 0/",
		configPath,
	)
	out, err := runBoundedOutput(cmd, 0)
	if err != nil {
		return fmt.Errorf("sed config.ini.php: %w (output: %s)", err, truncateStr(string(out), 256))
	}
	return nil
}

func matomoInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req matomoInstallReq
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
	if req.AdminUser == "" || !matomoAdminUserPattern.MatchString(req.AdminUser) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_user must be 3-100 chars of letters, digits, dot, dash, underscore, or @",
		}
	}
	if len(req.AdminPass) < 6 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "admin_pass must be at least 6 characters (Matomo minimum)",
		}
	}
	if req.AdminEmail == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "admin_email is required"}
	}
	if err := validateDocrootPath(req.OSUser, req.Docroot); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	installPath := computeMatomoInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		mkdirCmd := buildSystemdRunCmd(ctx, req.OSUser, "mkdir", "-p", installPath)
		if out, err := runBoundedOutput(mkdirCmd, 0); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mkdir %s: %v (output: %s)", installPath, err, truncateStr(string(out), 256))}
		}
	}

	removePlaceholderIndex(ctx, installPath)

	tmpDir, err := os.MkdirTemp("", "matomo-")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mktemp: %v", err)}
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tmpdir: %v", err)}
	}
	zipPath := filepath.Join(tmpDir, "matomo.zip")

	dlCtx, dlCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer dlCancel()
	if err := downloadMatomoZip(dlCtx, zipPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := verifyMatomoSHA256(zipPath); err != nil {
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

	if err := extractMatomoZip(ctx, req.OSUser, zipPath, installPath, stagingDir); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := writeMatomoConfig(ctx, req.OSUser, installPath, req); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := runMatomoCoreInstall(ctx, req.OSUser, installPath); err != nil {
		_ = exec.CommandContext(ctx, "rm", "-f", filepath.Join(installPath, "config", "config.ini.php")).Run()
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := runMatomoUserCreate(ctx, req, installPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := finalizeMatomoConfig(ctx, req.OSUser, installPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := normalizePermsToWwwData(ctx, installPath, req.OSUser); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	return matomoInstallResp{Version: matomoVersion}, nil
}

func init() {
	RegisterAppInstaller("matomo", matomoInstallHandler)
}
