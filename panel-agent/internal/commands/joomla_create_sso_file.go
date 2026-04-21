// joomla.create_sso_file — write a self-deleting SSO PHP file into a
// Joomla install's webroot. See ADR-0040.

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

//go:embed sso_template_joomla.php
var joomlaSSOTemplate string

var (
	// joomlaInstallPathRE matches an absolute install path with no ".."
	// component. Used as defence in depth before substituting into the
	// PHP template's JPATH_BASE define.
	joomlaInstallPathRE   = regexp.MustCompile(`^/[A-Za-z0-9_/.\-]+$`)
	joomlaInstallDotDotRE = regexp.MustCompile(`(^|/)\.\.(/|$)`)
	// joomlaAdminUserRE — Joomla's username allows more characters than
	// WordPress by default; we still restrict to the same safe set the
	// panel's generateAdminUsername produces (6 lowercase letters) plus
	// common admin-username variants.
	joomlaAdminUserRE = regexp.MustCompile(`^[A-Za-z0-9_.\-@][A-Za-z0-9 _.\-@]{0,58}[A-Za-z0-9_.\-@]$|^[A-Za-z0-9_.\-@]$`)
)

// RenderJoomlaSSOTemplate substitutes placeholders in the Joomla PHP
// template.
func RenderJoomlaSSOTemplate(nonce, installPath, installID, adminUsername string) (string, error) {
	if !nonceRE.MatchString(nonce) {
		return "", fmt.Errorf("invalid nonce: must be 43 base64url chars, got %q", nonce)
	}
	if joomlaInstallDotDotRE.MatchString(installPath) {
		return "", fmt.Errorf("invalid installPath: contains \"..\" component, got %q", installPath)
	}
	if !joomlaInstallPathRE.MatchString(installPath) {
		return "", fmt.Errorf("invalid installPath: must be absolute, contain only [A-Za-z0-9_/.\\-], got %q", installPath)
	}
	if !sedSafeULID(installID) {
		return "", fmt.Errorf("invalid installID: must be 26-char Crockford ULID, got %q", installID)
	}
	if !joomlaAdminUserRE.MatchString(adminUsername) {
		return "", fmt.Errorf("invalid adminUsername: must match [A-Za-z0-9 _.\\-@], 1-60 chars, no leading/trailing whitespace, got %q", adminUsername)
	}

	out := joomlaSSOTemplate
	out = strings.ReplaceAll(out, "__JABALI_TTL_SECONDS__", fmt.Sprintf("%d", ssoTTLSeconds))
	out = strings.ReplaceAll(out, "__JABALI_INSTALL_PATH__", phpStringLiteral(installPath))
	out = strings.ReplaceAll(out, "__JABALI_ADMIN_USERNAME__", phpStringLiteral(adminUsername))
	out = strings.ReplaceAll(out, "__JABALI_INSTALL_ID__", installID)
	out = strings.ReplaceAll(out, "__JABALI_NONCE__", nonce)

	if leftoverMarker.MatchString(out) {
		return "", fmt.Errorf("unsubstituted placeholder remains in rendered template")
	}
	return out, nil
}

// CreateJoomlaSSOFile renders the Joomla template and atomically writes
// it into the install's webroot.
func CreateJoomlaSSOFile(ctx context.Context, req createSSOFileReq) (createSSOFileResp, error) {
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

	// Joomla's bootstrap entry is includes/framework.php; it exists
	// inside the install root. Verify before rendering.
	framework := filepath.Join(req.InstallPath, "includes", "framework.php")
	if _, err := os.Stat(framework); err != nil {
		return zero, fmt.Errorf("includes/framework.php not found at %s: %w", framework, err)
	}

	nonce, err := GenerateNonce()
	if err != nil {
		return zero, fmt.Errorf("generate nonce: %w", err)
	}

	body, err := RenderJoomlaSSOTemplate(nonce, req.InstallPath, req.InstallID, req.AdminUsername)
	if err != nil {
		return zero, fmt.Errorf("render template: %w", err)
	}

	return writeSSOFile(ctx, req.InstallPath, req.OSUser, nonce, body)
}

func createJoomlaSSOFileHandler(ctx context.Context, payload json.RawMessage) (any, error) {
	var req createSSOFileReq
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid create_sso_file payload: %v", err),
		}
	}
	resp, err := CreateJoomlaSSOFile(ctx, req)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: err.Error(),
		}
	}
	return resp, nil
}

func init() {
	Default.Register("joomla.create_sso_file", createJoomlaSSOFileHandler)
}
