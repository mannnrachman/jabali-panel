// migration.import_home — copy an extracted cpmove home tree into a
// destination jabali user's /home/<user>/. M35 cPanel restore stage.
//
// Source path: /var/lib/jabali-migrations/<job-id>/extracted/cp/
//              <source-user>/homedir/
// Destination: /home/<target-user>/
//
// Mechanics:
// - rsync -aH (preserve perms/links/hardlinks) with delete-after on
//   the destination so resume after partial failure converges
// - chown -R <target>:<target> after rsync (cpmove tarballs preserve
//   source-side ownership which would point at numeric uids on the
//   source host)
// - excludes .ssh/ (panel-side ssh_keys table is the truth; reconciler
//   materialises authorized_keys), .cpanel/ (cPanel control files
//   meaningless on jabali), .htpasswd files (operator re-creates),
//   and standard backup-noise (.lock, .DS_Store, etc.)
// - 4-hour timeout per call; mid-call cancellation kills rsync
//
// SECURITY: only runs as root (agent is privileged). The dest_user
// argument is validated against /etc/passwd via getent before any
// rsync runs — refuses to write to a path that doesn't belong to a
// real user. The src argument MUST be inside /var/lib/jabali-
// migrations/ (path-prefix check). Both checks fail-closed with
// agentwire.CodeInvalidArgument.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

const (
	migrationStagingRoot = "/var/lib/jabali-migrations"
	migrationHomeTimeout = 4 * time.Hour
)

// migrationHomeExcludes is the rsync --exclude pattern set. .ssh/ is
// excluded because the panel's ssh_keys table is the truth on
// authorized_keys (reconciler writes); .cpanel/ is cPanel-only
// metadata; standard noise files round it out.
var migrationHomeExcludes = []string{
	".ssh/",
	".cpanel/",
	".htpasswd",
	".lock",
	".DS_Store",
	"public_html/.well-known/acme-challenge/",
	"tmp/",
}

type migrationImportHomeParams struct {
	JobID    string `json:"job_id"`
	SrcDir   string `json:"src_dir"`   // absolute, must live under migrationStagingRoot
	DestUser string `json:"dest_user"` // resolved via getent — must exist + have a home
}

type migrationImportHomeResult struct {
	BytesCopied int64  `json:"bytes_copied"`
	Files       int64  `json:"files"`
	DestPath    string `json:"dest_path"`
	Skipped     []string `json:"skipped,omitempty"`
}

func init() {
	Default.Register("migration.import_home", migrationImportHomeHandler)
}

func migrationImportHomeHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var p migrationImportHomeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "malformed JSON: " + err.Error(),
		}
	}
	if p.JobID == "" || p.SrcDir == "" || p.DestUser == "" {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "job_id, src_dir, dest_user required",
		}
	}

	// Path validation — refuse anything outside the staging root so
	// a malformed call can't pull from arbitrary host paths.
	srcAbs, err := filepath.Abs(p.SrcDir)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "src_dir not absolute: " + err.Error(),
		}
	}
	if !strings.HasPrefix(srcAbs+"/", migrationStagingRoot+"/") {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("src_dir must live under %s, got %q", migrationStagingRoot, srcAbs),
		}
	}

	// Resolve dest user via getent (system-truth). Refuse if the
	// user has no entry — restore-stage runner is responsible for
	// CreateUser before calling this.
	u, err := user.Lookup(p.DestUser)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("dest_user %q not found in /etc/passwd: %v", p.DestUser, err),
		}
	}
	if u.HomeDir == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("dest_user %q has no homedir", p.DestUser),
		}
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("getent returned non-numeric uid %q for %q", u.Uid, p.DestUser),
		}
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("getent returned non-numeric gid %q for %q", u.Gid, p.DestUser),
		}
	}

	subctx, cancel := context.WithTimeout(ctx, migrationHomeTimeout)
	defer cancel()

	// Build rsync argv. Trailing slash on source = copy contents
	// not the directory itself.
	// --no-h forces raw byte counts in the stats2 summary.
	// Without it rsync 3.x prints "Total transferred file size: 752.0M"
	// which parseRsyncStats can't parse as int → byte count
	// silently zeros (QA flagged: 752 MB restored, manifest
	// reported bytes=0).
	args := []string{"-aH", "--no-h", "--info=stats2", "--delete-after"}
	for _, ex := range migrationHomeExcludes {
		args = append(args, "--exclude="+ex)
	}
	srcWithSlash := strings.TrimRight(srcAbs, "/") + "/"
	args = append(args, srcWithSlash, u.HomeDir+"/")

	cmd := exec.CommandContext(subctx, "rsync", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("rsync failed: %v: %s", err, truncate(string(out), 4096)),
		}
	}

	bytesCopied, files := parseRsyncStats(string(out))

	// Chown the entire destination tree so files land owned by the
	// destination user even though the source tarball preserved
	// source-side numeric uids.
	chownCmd := exec.CommandContext(subctx, "chown", "-R",
		fmt.Sprintf("%d:%d", uid, gid), u.HomeDir)
	if cout, cerr := chownCmd.CombinedOutput(); cerr != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("chown failed: %v: %s", cerr, truncate(string(cout), 1024)),
		}
	}

	return migrationImportHomeResult{
		BytesCopied: bytesCopied,
		Files:       files,
		DestPath:    u.HomeDir,
	}, nil
}

// parseRsyncStats pulls byte + file counts from `rsync --info=stats2`
// stderr. Returns zeros when stats lines aren't present (very small
// transfers may skip the stats block).
func parseRsyncStats(stats string) (bytes, files int64) {
	for _, line := range strings.Split(stats, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Number of regular files transferred:"):
			fields := strings.Fields(line)
			if len(fields) > 0 {
				cleaned := strings.ReplaceAll(fields[len(fields)-1], ",", "")
				if v, err := strconv.ParseInt(cleaned, 10, 64); err == nil {
					files = v
				}
			}
		case strings.HasPrefix(line, "Total transferred file size:"):
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				cleaned := strings.ReplaceAll(fields[4], ",", "")
				if v, err := strconv.ParseInt(cleaned, 10, 64); err == nil {
					bytes = v
				}
			}
		}
	}
	return bytes, files
}

// truncate returns at most n bytes of s. Used to bound the size of
// rsync's stderr inside an AgentError so a stuck rsync producing
// MB of warnings doesn't blow up the JSON envelope.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}
