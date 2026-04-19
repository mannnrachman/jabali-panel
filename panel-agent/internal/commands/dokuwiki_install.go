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
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// dokuwikiInstallReq is the input shape for the DokuWiki installer.
// app.install forwards the body unchanged after reading app_type, so
// the same `os_user` / `docroot` / `subdirectory` / `site_url` /
// `use_www` fields the WordPress installer accepts arrive here too —
// just the per-app param block differs.
type dokuwikiInstallReq struct {
	AppType      string `json:"app_type"`     // present, ignored — dispatcher routed us
	OSUser       string `json:"os_user"`
	Docroot      string `json:"docroot"`
	Subdirectory string `json:"subdirectory"`
	SiteURL      string `json:"site_url"`     // unused by the installer; kept for symmetry
	UseWWW       bool   `json:"use_www"`      // unused; kept for symmetry
	SiteTitle    string `json:"site_title"`
	AdminUser    string `json:"admin_user"`   // panel maps params.admin_username -> admin_user
	AdminPass    string `json:"admin_pass"`   // panel maps params.admin_password -> admin_pass
	AdminEmail   string `json:"admin_email"`
	License      string `json:"license"`
}

type dokuwikiInstallResp struct {
	// Version reported back as "stable" — the upstream tarball doesn't
	// embed a release date the agent can extract without parsing
	// VERSION, which moves per release. The reconciler treats any
	// non-empty version as "ready"; "stable" is a meaningful enough
	// marker until a follow-up reads conf/.../VERSION.
	Version string `json:"version"`
}

// dokuwikiTarballURL points at the always-current stable release.
// DokuWiki publishes "stable" as a moving target; the install pinning
// is via the SHA-256 below — bump both together when a new release
// comes out.
const dokuwikiTarballURL = "https://download.dokuwiki.org/src/dokuwiki/dokuwiki-stable.tgz"

// dokuwikiTarballSHA256 is the SHA-256 of the tarball at
// dokuwikiTarballURL as of the install-time pin. Update when bumping
// to a new upstream release; an empty value disables the integrity
// check (DEV ONLY — keep the constant non-empty in shipped builds).
//
// To compute on a host with the URL accessible:
//
//	curl -sSL -A 'jabali-panel-agent/1.0 (+https://jabali.local)' \
//	  https://download.dokuwiki.org/src/dokuwiki/dokuwiki-stable.tgz \
//	  | sha256sum
//
// Bump alongside the version pin when DokuWiki ships a new stable.
// Captured 2026-04-19 from upstream "stable" — DokuWiki publishes
// "stable" as a moving target, so this needs re-capturing whenever
// the upstream rolls a new release. The integrity check fails-loud
// rather than silently downloading a different tarball.
const dokuwikiTarballSHA256 = "1d10e8dc8ad769b1c56a53a8703db9345070663e8386ee6bded77d4881d090f3"

// dokuwikiLicenseURLs maps the descriptor's license enum to the
// canonical license URL DokuWiki writes into conf/local.php. A blank
// entry means "no license declared".
var dokuwikiLicenseURLs = map[string]struct {
	Name string
	URL  string
}{
	"cc-by-sa":      {"CC Attribution-Share Alike 4.0 International", "https://creativecommons.org/licenses/by-sa/4.0/"},
	"cc-by-nc-sa":   {"CC Attribution-Noncommercial-Share Alike 4.0 International", "https://creativecommons.org/licenses/by-nc-sa/4.0/"},
	"public-domain": {"Public Domain", "https://creativecommons.org/publicdomain/zero/1.0/"},
	"gpl":           {"GNU GPL 3.0", "https://www.gnu.org/licenses/gpl.html"},
	"none":          {"", ""},
}

// computeDokuWikiInstallPath joins docroot + subdir, returning an
// absolute path inside the docroot. Caller is responsible for
// validateDocrootPath BEFORE invoking this — this helper does no
// boundary check, only normalisation.
func computeDokuWikiInstallPath(docroot, subdirectory string) string {
	if subdirectory == "" {
		return docroot
	}
	return filepath.Join(docroot, subdirectory)
}

// downloadDokuWikiTarball fetches the upstream stable tarball into a
// caller-owned tempfile. Returns the on-disk path; caller must
// `os.Remove` it after extraction.
func downloadDokuWikiTarball(ctx context.Context, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dokuwikiTarballURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	// Identify ourselves to upstreams — Wikimedia rejects the Go
	// default UA; DokuWiki accepts it but being polite is cheap.
	// userAgent is defined in mediawiki_install.go.
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", dokuwikiTarballURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", dokuwikiTarballURL, resp.StatusCode)
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

// verifyDokuWikiSHA256 returns nil when the file at path matches the
// pinned SHA-256, an error otherwise. An empty pin (DEV) skips the
// check with no error — log loudly upstream when shipping.
func verifyDokuWikiSHA256(path string) error {
	if dokuwikiTarballSHA256 == "" {
		// Intentionally permissive in dev builds; production should
		// always set the constant. Returning nil here matches the
		// "build can ship without a pinned tarball checksum" choice.
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
	if !strings.EqualFold(got, dokuwikiTarballSHA256) {
		return fmt.Errorf("dokuwiki tarball sha256 mismatch: got %s want %s", got, dokuwikiTarballSHA256)
	}
	return nil
}

// extractDokuWikiTarball untars the tarball into installPath. The
// upstream tarball wraps content under a top-level dokuwiki-YYYY-MM-DD
// directory; tar's --strip-components=1 flattens that out so files
// land directly under installPath/conf, installPath/data, etc.
func extractDokuWikiTarball(ctx context.Context, osUser, tarballPath, installPath string) error {
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

// buildDokuWikiLocalPHP renders conf/local.php with the user's
// site title + license selection. Single-quoted strings are escaped
// per phpEscapeSingleQuoted so a quote in the title can't end the
// string early.
func buildDokuWikiLocalPHP(siteTitle, license string) string {
	licenseEntry := dokuwikiLicenseURLs[license]
	var b strings.Builder
	b.WriteString("<?php\n")
	b.WriteString("/**\n * Generated by jabali-agent at install time. Edit via the\n * Configuration Manager in the wiki admin UI; this file is rewritten\n * only on a re-install.\n */\n")
	fmt.Fprintf(&b, "$conf['title'] = '%s';\n", phpEscapeSingleQuoted(siteTitle))
	fmt.Fprintf(&b, "$conf['license'] = '%s';\n", phpEscapeSingleQuoted(license))
	if licenseEntry.URL != "" {
		fmt.Fprintf(&b, "$conf['license_url'] = '%s';\n", phpEscapeSingleQuoted(licenseEntry.URL))
		fmt.Fprintf(&b, "$conf['license_name'] = '%s';\n", phpEscapeSingleQuoted(licenseEntry.Name))
	}
	// useacl=1 enables the per-user ACL system the admin user needs to
	// log in and manage the wiki. Without it, every page would be
	// world-editable (the historic DokuWiki out-of-box behaviour).
	b.WriteString("$conf['useacl'] = 1;\n")
	b.WriteString("$conf['superuser'] = '@admin';\n")
	return b.String()
}

// buildDokuWikiUsersAuth renders a single admin entry for
// conf/users.auth.php. DokuWiki accepts smd5 / sha1 / bcrypt prefixes;
// we use bcrypt via $2y$ which DokuWiki's authplain backend understands
// since release 2014-05-05 "Ponder Stibbons".
//
// Format: login:hash:Real Name:email:groups,comma-separated
func buildDokuWikiUsersAuth(adminUser, adminPassHash, adminEmail string) string {
	return fmt.Sprintf("# users.auth.php\n# <login>:<password>:<Real Name>:<email>:<groups>\n%s:%s:Administrator:%s:admin,user\n",
		adminUser, adminPassHash, adminEmail)
}

// buildDokuWikiACLAuth renders a sane default ACL: admin gets the
// full read/edit/create/delete/upload permission, anonymous gets
// read-only, registered users get edit. Mirrors the policy the
// DokuWiki web wizard would have set when prompted "Open wiki" with
// the admin login filled in.
func buildDokuWikiACLAuth(adminUser string) string {
	return fmt.Sprintf(`# acl.auth.php
# <page or namespace>	<user or @group>	<permission>
*	@ALL	1
*	@user	8
*	@admin	16
%s	@admin	16
`, adminUser)
}

// phpEscapeSingleQuoted prepares a Go string for embedding inside a
// PHP single-quoted literal: backslash and single-quote are the only
// metacharacters PHP recognises in '…'.
func phpEscapeSingleQuoted(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

// hashPasswordForDokuWiki produces a bcrypt $2y$10$… hash that
// DokuWiki's authplain backend recognises. Uses PHP-side bcrypt via
// the agent's existing wp-cli pattern: shell out to `php -r` so we
// don't pull in golang.org/x/crypto/bcrypt for a one-shot hash AND
// match exactly what DokuWiki's password_hash() would produce. The
// $2y$ prefix is what password_hash(PASSWORD_DEFAULT) emits; DokuWiki
// detects it and dispatches to password_verify().
func hashPasswordForDokuWiki(ctx context.Context, password string) (string, error) {
	// Pass the password via stdin so it never appears on the process
	// command line (no /proc/<pid>/cmdline leak).
	cmd := exec.CommandContext(ctx, "php", "-r",
		`echo password_hash(stream_get_contents(STDIN), PASSWORD_BCRYPT, ['cost' => 10]);`)
	cmd.Stdin = strings.NewReader(password)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("php password_hash: %w", err)
	}
	hash := strings.TrimSpace(string(out))
	if !strings.HasPrefix(hash, "$2y$") {
		return "", fmt.Errorf("php password_hash returned unexpected prefix: %q", truncateStr(hash, 16))
	}
	return hash, nil
}

// writeDokuWikiConfigFile writes data to dst as the install owner so
// FPM (which runs AS the user) can rewrite it later via the Config
// Manager UI. Group is www-data + 0640 so nginx can read it; matches
// normalizePermsToWwwData's contract.
func writeDokuWikiConfigFile(ctx context.Context, osUser, dst, data string) error {
	if err := os.WriteFile(dst, []byte(data), 0o640); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	if err := exec.CommandContext(ctx, "chown", osUser+":www-data", dst).Run(); err != nil {
		return fmt.Errorf("chown %s: %w", dst, err)
	}
	return nil
}

func dokuwikiInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req dokuwikiInstallReq
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}
	if req.OSUser == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "os_user is required"}
	}
	if req.Docroot == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "docroot is required"}
	}
	if req.AdminUser == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "admin_user is required"}
	}
	if req.AdminPass == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "admin_pass is required"}
	}
	if req.AdminEmail == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "admin_email is required"}
	}
	if _, ok := dokuwikiLicenseURLs[req.License]; !ok {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("unknown license %q", req.License),
		}
	}
	if err := validateDocrootPath(req.OSUser, req.Docroot); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	installPath := computeDokuWikiInstallPath(req.Docroot, req.Subdirectory)

	// Make sure installPath exists, owned by the install user. domain.create
	// creates the docroot itself; subdirectory installs need an mkdir under
	// it before tar can extract.
	if req.Subdirectory != "" {
		mkdirCmd := buildSystemdRunCmd(ctx, req.OSUser, "mkdir", "-p", installPath)
		if out, err := mkdirCmd.CombinedOutput(); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("mkdir %s: %v (output: %s)", installPath, err, truncateStr(string(out), 256)),
			}
		}
	}

	// Drop the placeholder index.html domain.create writes. nginx serves
	// it before doku.php otherwise.
	removePlaceholderIndex(ctx, installPath)

	// Download to a temp file owned by the agent (root). Extraction runs
	// as the install user via systemd-run so the resulting files land
	// with the right uid; the tarball itself doesn't need user ownership.
	tmpDir, err := os.MkdirTemp("", "dokuwiki-")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("mktemp: %v", err)}
	}
	defer os.RemoveAll(tmpDir)
	// MkdirTemp creates the dir 0700 root:root. The systemd-run-as-user
	// `tar` below can't traverse it to reach the tarball inside. Widen
	// to 0755 so the per-domain user can chdir/open the file (the file
	// itself is chmod'd 0644 below). Tarball contains no secrets.
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tmpdir: %v", err)}
	}
	tarballPath := filepath.Join(tmpDir, "dokuwiki-stable.tgz")

	dlCtx, dlCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer dlCancel()
	if err := downloadDokuWikiTarball(dlCtx, tarballPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := verifyDokuWikiSHA256(tarballPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	// Make tarball world-readable so the systemd-run-as-user tar can
	// open it. Temp file under /tmp owned by root with 0600 by default.
	if err := os.Chmod(tarballPath, 0o644); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chmod tarball: %v", err)}
	}

	if err := extractDokuWikiTarball(ctx, req.OSUser, tarballPath, installPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	// Configure the wiki: site title, license, admin user, ACL.
	confDir := filepath.Join(installPath, "conf")
	if err := writeDokuWikiConfigFile(ctx, req.OSUser, filepath.Join(confDir, "local.php"),
		buildDokuWikiLocalPHP(req.SiteTitle, req.License)); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	hash, err := hashPasswordForDokuWiki(ctx, req.AdminPass)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := writeDokuWikiConfigFile(ctx, req.OSUser, filepath.Join(confDir, "users.auth.php"),
		buildDokuWikiUsersAuth(req.AdminUser, hash, req.AdminEmail)); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if err := writeDokuWikiConfigFile(ctx, req.OSUser, filepath.Join(confDir, "acl.auth.php"),
		buildDokuWikiACLAuth(req.AdminUser)); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	// install.lock tells DokuWiki the install wizard has run — without
	// it, every page request redirects to install.php. Empty file is
	// enough; DokuWiki only checks for existence.
	lockPath := filepath.Join(installPath, "install.lock")
	if err := writeDokuWikiConfigFile(ctx, req.OSUser, lockPath, ""); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	if err := normalizePermsToWwwData(ctx, installPath, req.OSUser); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	return dokuwikiInstallResp{Version: "stable"}, nil
}

func init() {
	RegisterAppInstaller("dokuwiki", dokuwikiInstallHandler)
}
