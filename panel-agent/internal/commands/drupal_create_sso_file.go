// drupal.create_sso_file — write a self-deleting SSO PHP file into a
// Drupal install's webroot. See ADR-0040.

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

//go:embed sso_template_drupal.php
var drupalSSOTemplate string

var (
	// drupalAutoloadPathRE matches an absolute path ending in
	// /autoload.php with no ".." components. Agent validates before
	// substituting into the template.
	drupalAutoloadPathRE = regexp.MustCompile(`^/[A-Za-z0-9_/.\-]+/autoload\.php$`)
	// drupalAutoloadDotDotRE mirrors the wpLoadDotDotRE defence.
	drupalAutoloadDotDotRE = regexp.MustCompile(`(^|/)\.\.(/|$)`)
	// drupalAdminUserRE — Drupal's username rules. The schema allows
	// most printable characters but the panel's generateAdminUsername
	// produces 6 lowercase letters; we reject anything more exotic than
	// the WordPress alphabet to keep the defence aligned across CMSes.
	drupalAdminUserRE = regexp.MustCompile(`^[A-Za-z0-9_.\-@][A-Za-z0-9 _.\-@]{0,58}[A-Za-z0-9_.\-@]$|^[A-Za-z0-9_.\-@]$`)
)

// RenderDrupalSSOTemplate substitutes placeholders in the Drupal PHP
// template.
func RenderDrupalSSOTemplate(nonce, autoloadPath, installID, adminUsername string) (string, error) {
	if !nonceRE.MatchString(nonce) {
		return "", fmt.Errorf("invalid nonce: must be 43 base64url chars, got %q", nonce)
	}
	if drupalAutoloadDotDotRE.MatchString(autoloadPath) {
		return "", fmt.Errorf("invalid autoloadPath: contains \"..\" component, got %q", autoloadPath)
	}
	if !drupalAutoloadPathRE.MatchString(autoloadPath) {
		return "", fmt.Errorf("invalid autoloadPath: must be absolute, end in autoload.php, contain only [A-Za-z0-9_/.\\-], got %q", autoloadPath)
	}
	if !sedSafeULID(installID) {
		return "", fmt.Errorf("invalid installID: must be 26-char Crockford ULID, got %q", installID)
	}
	if !drupalAdminUserRE.MatchString(adminUsername) {
		return "", fmt.Errorf("invalid adminUsername: must match [A-Za-z0-9 _.\\-@], 1-60 chars, no leading/trailing whitespace, got %q", adminUsername)
	}

	out := drupalSSOTemplate
	out = strings.ReplaceAll(out, "__JABALI_TTL_SECONDS__", fmt.Sprintf("%d", ssoTTLSeconds))
	out = strings.ReplaceAll(out, "__JABALI_AUTOLOAD_PATH__", phpStringLiteral(autoloadPath))
	out = strings.ReplaceAll(out, "__JABALI_ADMIN_USERNAME__", phpStringLiteral(adminUsername))
	out = strings.ReplaceAll(out, "__JABALI_INSTALL_ID__", installID)
	out = strings.ReplaceAll(out, "__JABALI_NONCE__", nonce)

	if leftoverMarker.MatchString(out) {
		return "", fmt.Errorf("unsubstituted placeholder remains in rendered template")
	}
	return out, nil
}

// CreateDrupalSSOFile renders the Drupal template and atomically writes
// it into the install's webroot.
func CreateDrupalSSOFile(ctx context.Context, req createSSOFileReq) (createSSOFileResp, error) {
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

	// Drupal's autoload.php is always at the install root (unlike WP
	// where wp-load.php can drift). No walking needed.
	autoloadPath := filepath.Join(req.InstallPath, "autoload.php")
	if _, err := os.Stat(autoloadPath); err != nil {
		return zero, fmt.Errorf("autoload.php not found at %s: %w", autoloadPath, err)
	}

	nonce, err := GenerateNonce()
	if err != nil {
		return zero, fmt.Errorf("generate nonce: %w", err)
	}

	body, err := RenderDrupalSSOTemplate(nonce, autoloadPath, req.InstallID, req.AdminUsername)
	if err != nil {
		return zero, fmt.Errorf("render template: %w", err)
	}

	return writeSSOFile(ctx, req.InstallPath, req.OSUser, nonce, body)
}

func createDrupalSSOFileHandler(ctx context.Context, payload json.RawMessage) (any, error) {
	var req createSSOFileReq
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid create_sso_file payload: %v", err),
		}
	}
	resp, err := CreateDrupalSSOFile(ctx, req)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: err.Error(),
		}
	}
	return resp, nil
}

func init() {
	Default.Register("drupal.create_sso_file", createDrupalSSOFileHandler)
}
