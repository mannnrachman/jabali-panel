// wordpress.create_sso_file — write a self-deleting SSO PHP file into a
// WordPress install's webroot. See ADR-0040.

package commands

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

//go:embed sso_template_wordpress.php
var wordpressSSOTemplate string

var (
	wpLoadPathRE   = regexp.MustCompile(`^/[A-Za-z0-9_/.\-]+/wp-load\.php$`)
	wpLoadDotDotRE = regexp.MustCompile(`(^|/)\.\.(/|$)`)
	// wpAdminUserRE accepts the WordPress username alphabet.
	// wp_validate_username permits letters/digits/space/_/./-/@ up to
	// 60 chars. We additionally reject leading/trailing whitespace
	// because such a username can't be created via the WP admin UI.
	wpAdminUserRE = regexp.MustCompile(`^[A-Za-z0-9_.\-@][A-Za-z0-9 _.\-@]{0,58}[A-Za-z0-9_.\-@]$|^[A-Za-z0-9_.\-@]$`)
)

// createSSOFileReq is the input shape for every per-CMS create_sso_file
// command. Shared so panel-api builds one payload regardless of
// app_type.
type createSSOFileReq struct {
	InstallPath   string `json:"install_path"`
	OSUser        string `json:"os_user"`
	InstallID     string `json:"install_id"`
	AdminUsername string `json:"admin_username"`
}

// RenderWordPressSSOTemplate substitutes placeholders in the WordPress
// PHP template. All inputs are validated; on any failure returns an
// empty string and a clear error.
//
// adminUsername is the WordPress username — the PHP file resolves it to
// a WP_User via get_user_by('login', ...).
func RenderWordPressSSOTemplate(nonce, wpLoadPath, installID, adminUsername string) (string, error) {
	if !nonceRE.MatchString(nonce) {
		return "", fmt.Errorf("invalid nonce: must be 43 base64url chars, got %q", nonce)
	}
	if wpLoadDotDotRE.MatchString(wpLoadPath) {
		return "", fmt.Errorf("invalid wpLoadPath: contains \"..\" component, got %q", wpLoadPath)
	}
	if !wpLoadPathRE.MatchString(wpLoadPath) {
		return "", fmt.Errorf("invalid wpLoadPath: must be absolute, end in wp-load.php, contain only [A-Za-z0-9_/.\\-], got %q", wpLoadPath)
	}
	if !sedSafeULID(installID) {
		return "", fmt.Errorf("invalid installID: must be 26-char Crockford ULID, got %q", installID)
	}
	if !wpAdminUserRE.MatchString(adminUsername) {
		return "", fmt.Errorf("invalid adminUsername: must match WordPress username alphabet [A-Za-z0-9 _.\\-@], 1-60 chars, no leading/trailing whitespace, got %q", adminUsername)
	}

	out := wordpressSSOTemplate
	out = strings.ReplaceAll(out, "__JABALI_TTL_SECONDS__", fmt.Sprintf("%d", ssoTTLSeconds))
	out = strings.ReplaceAll(out, "__JABALI_WP_LOAD_PATH__", phpStringLiteral(wpLoadPath))
	out = strings.ReplaceAll(out, "__JABALI_ADMIN_USERNAME__", phpStringLiteral(adminUsername))
	out = strings.ReplaceAll(out, "__JABALI_INSTALL_ID__", installID)
	out = strings.ReplaceAll(out, "__JABALI_NONCE__", nonce)

	if leftoverMarker.MatchString(out) {
		return "", fmt.Errorf("unsubstituted placeholder remains in rendered template")
	}
	return out, nil
}

// CreateWordPressSSOFile renders the embedded WordPress PHP template,
// writes it atomically to <install_path>/jabali-sso-<nonce>.php, chowns
// to <os_user>:www-data, chmods to 0440, and returns the file name.
func CreateWordPressSSOFile(ctx context.Context, req createSSOFileReq) (createSSOFileResp, error) {
	zero := createSSOFileResp{}

	if !filepath.IsAbs(req.InstallPath) {
		return zero, fmt.Errorf("install_path must be absolute, got %q", req.InstallPath)
	}
	if req.OSUser == "" {
		return zero, fmt.Errorf("os_user required")
	}
	if !sedSafeULID(req.InstallID) {
		return zero, fmt.Errorf("install_id must be 26-char Crockford ULID, got %q", req.InstallID)
	}

	// Resolve wp-load.php: try install_path first, then walk up two
	// levels. Subdirectory installs may have it one level deeper than
	// expected.
	wpLoadPath, err := resolveWPLoadPath(req.InstallPath)
	if err != nil {
		return zero, err
	}

	nonce, err := GenerateNonce()
	if err != nil {
		return zero, fmt.Errorf("generate nonce: %w", err)
	}

	body, err := RenderWordPressSSOTemplate(nonce, wpLoadPath, req.InstallID, req.AdminUsername)
	if err != nil {
		return zero, fmt.Errorf("render template: %w", err)
	}

	return writeSSOFile(ctx, req.InstallPath, req.OSUser, nonce, body)
}

// resolveWPLoadPath looks for wp-load.php at installPath, then one
// level up, then two. Subdirectory WP installs may be configured with
// the docroot pointing at a parent of the WP root.
func resolveWPLoadPath(installPath string) (string, error) {
	candidate := filepath.Join(installPath, "wp-load.php")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	dir := installPath
	for i := 0; i < 2; i++ {
		dir = filepath.Dir(dir)
		candidate = filepath.Join(dir, "wp-load.php")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("wp-load.php not found at or above %s", installPath)
}

func createWordPressSSOFileHandler(ctx context.Context, payload json.RawMessage) (any, error) {
	var req createSSOFileReq
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid create_sso_file payload: %v", err),
		}
	}
	resp, err := CreateWordPressSSOFile(ctx, req)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: err.Error(),
		}
	}
	return resp, nil
}

func init() {
	Default.Register("wordpress.create_sso_file", createWordPressSSOFileHandler)
}
