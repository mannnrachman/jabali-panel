// wordpress.reap_sso_files — sweep stranded jabali-sso-*.php files older
// than the SSO TTL. Defence in depth alongside the inline TTL check in
// the PHP file (ADR-0040 T3).

package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// reapSSOFilesReq is the input shape for wordpress.reap_sso_files. The
// panel-side caller supplies the list of WP install paths to scan
// (typically all `application_installs` rows where app_type='wordpress'
// and status IN ('ready','installing','failed')). The agent does NOT
// query the panel DB itself; this keeps the agent stateless and lets
// the reaper handle subdirectory installs natively (the install row's
// path is the actual docroot the SSO file lives in).
type reapSSOFilesReq struct {
	InstallPaths []string `json:"install_paths"`
}

type reapSSOFilesResp struct {
	DeletedCount int `json:"deleted_count"`
	ScannedCount int `json:"scanned_count"`
}

// strict pattern: jabali-sso-<43 base64url chars>.php — exact length
// and alphabet. Prevents the reaper from deleting a hostile lookalike
// like "jabali-sso-evil.php" that a user might create in their own
// webroot to confuse us. (ADR-0040 T6 / adversarial review finding #6.)
var sssoFilePattern = regexp.MustCompile(`^jabali-sso-[A-Za-z0-9_-]{43}\.php$`)

// reapStaleAfter is the inline mtime cutoff. Files older than this are
// candidates for deletion. Matches the TTL the PHP file enforces.
const reapStaleAfter = time.Duration(ssoTTLSeconds) * time.Second

// ReapSSOFiles walks each install path non-recursively, filters by the
// strict regex, and deletes files whose mtime is older than the TTL.
// Permission errors on individual files are logged but do not fail the
// sweep — a single bad file shouldn't stop the rest from being cleaned.
//
// Returns scanned + deleted counts for observability.
func ReapSSOFiles(ctx context.Context, req reapSSOFilesReq) (reapSSOFilesResp, error) {
	now := time.Now()
	cutoff := now.Add(-reapStaleAfter)
	scanned := 0
	deleted := 0
	staleHigh := 0

	for _, dir := range req.InstallPaths {
		if !filepath.IsAbs(dir) {
			slog.WarnContext(ctx, "sso reaper: skipping non-absolute path", "path", dir)
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			// install dir gone (deleted between query and reap) is fine
			if os.IsNotExist(err) {
				continue
			}
			slog.WarnContext(ctx, "sso reaper: ReadDir failed, skipping", "path", dir, "err", err)
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !sssoFilePattern.MatchString(name) {
				continue
			}
			scanned++
			full := filepath.Join(dir, name)
			info, err := e.Info()
			if err != nil {
				slog.WarnContext(ctx, "sso reaper: stat failed, skipping", "file", full, "err", err)
				continue
			}
			if info.ModTime().After(cutoff) {
				continue
			}
			staleHigh++
			if err := os.Remove(full); err != nil {
				if !os.IsNotExist(err) {
					slog.WarnContext(ctx, "sso reaper: remove failed, skipping", "file", full, "err", err)
				}
				continue
			}
			deleted++
		}
	}

	if staleHigh > 100 {
		slog.WarnContext(ctx, "sso reaper: high stale count — investigate",
			"stale", staleHigh, "scanned", scanned, "deleted", deleted)
	}

	return reapSSOFilesResp{
		DeletedCount: deleted,
		ScannedCount: scanned,
	}, nil
}

func reapSSOFilesHandler(ctx context.Context, payload json.RawMessage) (any, error) {
	var req reapSSOFilesReq
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid reap_sso_files payload: %v", err),
		}
	}
	return ReapSSOFiles(ctx, req)
}

func init() {
	Default.Register("wordpress.reap_sso_files", reapSSOFilesHandler)
}
