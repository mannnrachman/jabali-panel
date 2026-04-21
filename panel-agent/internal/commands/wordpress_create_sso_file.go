// wordpress.create_sso_file — write a self-deleting SSO PHP file into a
// WordPress install's webroot. See ADR-0040.

package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// createSSOFileReq is the input shape for wordpress.create_sso_file.
type createSSOFileReq struct {
	// InstallPath is the WordPress install's web-served directory
	// (e.g. /home/shukivaknin/domains/123123.com/public_html). The agent
	// resolves wp-load.php starting here and walking up. The SSO file
	// is written here so that https://<site>/jabali-sso-<nonce>.php
	// resolves via nginx's existing PHP location block.
	InstallPath string `json:"install_path"`
	// OSUser is the install owner; the SSO file is chown'd to
	// <OSUser>:www-data so PHP-FPM (group www-data) can read it but
	// the user can't accidentally edit it.
	OSUser string `json:"os_user"`
	// InstallID is the application_installs row's ULID. Embedded in
	// the file's comment + error_log lines for grep-ability.
	InstallID string `json:"install_id"`
	// AdminUsername is the WP login the file resolves to a WP_User via
	// get_user_by('login', ...) at file-execution time. Baked in at
	// write time from the install row's admin_username column.
	AdminUsername string `json:"admin_username"`
}

// createSSOFileResp is the output shape for wordpress.create_sso_file.
type createSSOFileResp struct {
	// FileName is jabali-sso-<43chars>.php — what the panel-api
	// concatenates onto the site URL.
	FileName string `json:"file_name"`
	// ExpiresAtUnix is now() + 60s; the panel-api returns this in the
	// {url, expires_in} mint response.
	ExpiresAtUnix int64 `json:"expires_at_unix"`
}

// CreateSSOFile renders the embedded SSO PHP template, writes it
// atomically to <install_path>/jabali-sso-<nonce>.php, chown's to
// <os_user>:www-data, chmod's to 0440, and returns the file name.
//
// Cleanup-on-error guarantee (ADR-0040, plan §3 task 7): if any step
// after the rename succeeds but a later step (chown/chmod) fails, the
// file is removed before returning so no partial / wrong-owner file is
// left readable on disk.
func CreateSSOFile(ctx context.Context, req createSSOFileReq) (createSSOFileResp, error) {
	zero := createSSOFileResp{}

	// Validate inputs early.
	if !filepath.IsAbs(req.InstallPath) {
		return zero, fmt.Errorf("install_path must be absolute, got %q", req.InstallPath)
	}
	if req.OSUser == "" {
		return zero, fmt.Errorf("os_user required")
	}
	if !sedSafeULID(req.InstallID) {
		return zero, fmt.Errorf("install_id must be 26-char Crockford ULID, got %q", req.InstallID)
	}
	// adminUsername is validated inside RenderSSOTemplate; a separate
	// check here would duplicate the regex without adding value.

	// Resolve wp-load.php: try install_path first, then walk up two
	// levels. WP installed at the docroot has wp-load.php there;
	// subdirectory installs may have it one level deeper than expected.
	wpLoadPath, err := resolveWPLoadPath(req.InstallPath)
	if err != nil {
		return zero, err
	}

	nonce, err := GenerateNonce()
	if err != nil {
		return zero, fmt.Errorf("generate nonce: %w", err)
	}

	body, err := RenderSSOTemplate(nonce, wpLoadPath, req.InstallID, req.AdminUsername)
	if err != nil {
		return zero, fmt.Errorf("render template: %w", err)
	}

	fileName := "jabali-sso-" + nonce + ".php"
	dest := filepath.Join(req.InstallPath, fileName)
	tmp := filepath.Join(req.InstallPath, "."+fileName+".tmp")

	// Atomic write: open with O_CREAT|O_EXCL|O_WRONLY so a colliding
	// tmp name (impossible at 256-bit entropy but cheap to guard) fails
	// loudly rather than silently overwriting.
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return zero, fmt.Errorf("open tmp %s: %w", tmp, err)
	}
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return zero, fmt.Errorf("write tmp %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return zero, fmt.Errorf("fsync tmp %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return zero, fmt.Errorf("close tmp %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return zero, fmt.Errorf("rename %s -> %s: %w", tmp, dest, err)
	}

	// Cleanup-on-error: if either chown or chmod fails, remove the file
	// before returning. Prevents a partial-state file with the wrong
	// owner / mode from being left readable in the webroot.
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(dest)
		}
	}()

	if err := exec.CommandContext(ctx, "chown", req.OSUser+":www-data", dest).Run(); err != nil {
		return zero, fmt.Errorf("chown %s:www-data %s: %w", req.OSUser, dest, err)
	}
	if err := os.Chmod(dest, 0o440); err != nil {
		return zero, fmt.Errorf("chmod 0440 %s: %w", dest, err)
	}
	committed = true

	return createSSOFileResp{
		FileName:      fileName,
		ExpiresAtUnix: time.Now().Add(time.Duration(ssoTTLSeconds) * time.Second).Unix(),
	}, nil
}

// resolveWPLoadPath looks for wp-load.php at installPath, then one level
// up, then two levels up. Subdirectory WP installs may be configured
// with the docroot pointing at a parent of the WP root.
func resolveWPLoadPath(installPath string) (string, error) {
	candidate := filepath.Join(installPath, "wp-load.php")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	// Search up to 2 levels up.
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

// createSSOFileHandler is the wire dispatcher for wordpress.create_sso_file.
func createSSOFileHandler(ctx context.Context, payload json.RawMessage) (any, error) {
	var req createSSOFileReq
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid create_sso_file payload: %v", err),
		}
	}
	resp, err := CreateSSOFile(ctx, req)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: err.Error(),
		}
	}
	return resp, nil
}

func init() {
	Default.Register("wordpress.create_sso_file", createSSOFileHandler)
}
