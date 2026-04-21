// Shared atomic-write helper for M22 SSO files (ADR-0040).
//
// Called by every per-CMS create_sso_file handler. The helper owns the
// atomic-rename / chown / chmod / cleanup-on-error dance so each CMS's
// handler only concerns itself with rendering its own PHP template.

package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// createSSOFileResp is the wire response shape every per-CMS handler
// returns. Shared because the panel-api side uses the same decoder
// regardless of app_type — only the mint endpoint's dispatch decides
// which agent command it calls.
type createSSOFileResp struct {
	FileName      string `json:"file_name"`
	ExpiresAtUnix int64  `json:"expires_at_unix"`
}

// writeSSOFile writes body to <installPath>/jabali-sso-<nonce>.php
// atomically (tmp + rename), chowns to <osUser>:www-data, chmods to
// 0440, and returns the file name. If chown/chmod fail, removes the
// file before returning so no partial-state file is left readable.
//
// Called by the per-CMS create_sso_file handlers (wordpress, drupal,
// joomla, …). Each caller is responsible for rendering body from its
// own template first.
func writeSSOFile(ctx context.Context, installPath, osUser, nonce, body string) (createSSOFileResp, error) {
	zero := createSSOFileResp{}
	fileName := "jabali-sso-" + nonce + ".php"
	dest := filepath.Join(installPath, fileName)
	tmp := filepath.Join(installPath, "."+fileName+".tmp")

	// Atomic write: open with O_CREAT|O_EXCL so a colliding tmp name
	// (impossible at 256-bit entropy but cheap to guard) fails loudly.
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

	if err := exec.CommandContext(ctx, "chown", osUser+":www-data", dest).Run(); err != nil {
		return zero, fmt.Errorf("chown %s:www-data %s: %w", osUser, dest, err)
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
