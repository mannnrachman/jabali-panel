package cpanel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// PkgacctTimeout is the upper bound for the source-side pkgacct run.
// Real cPanel accounts take 5-30 min; the cap is generous because
// the failure mode of an under-budget timeout is "have to start over"
// vs the cost of waiting on a stuck pkgacct (zero — the transient
// unit dies + the operator restarts the job).
const PkgacctTimeout = 90 * time.Minute

// PullChunk caps a single SSH-stream cat into local disk. 1 GiB
// chunk size — io.Copy itself paces, this is the maximum buffered
// in any single Read.
const PullChunk = 1 << 20 // 1 MiB

// Pkgacct invokes /scripts/pkgacct <account> on the source host.
// Requires PrincipalAdmin (root) — pkgacct refuses to run as a
// non-root user. Returns the path to the produced tarball on the
// source side.
//
// pkgacct's output by default lands at /home/cpmove-<user>.tar.gz
// (uncompressed v1: cPanel honours the --backup flag for an
// alternate path; we don't override). Stalls on disk-full + a few
// other infra failures; the timeout above is the bound.
// RemoveRemote rm's a single absolute path on the source over the
// existing SSH session. Used after PullFile to clean the source-side
// cpmove tarball — pkgacct leaves `/home/cpmove-<user>.tar.gz`
// otherwise + a multi-account migration accumulates GB on the source
// box. Path must be absolute + match the safe-prefix allowlist.
func (d *Discoverer) RemoveRemote(ctx context.Context, raw interface{}, path string) error {
	s, ok := raw.(*session)
	if !ok || s == nil {
		return errors.New("RemoveRemote: bad session")
	}
	// Allowlist — only paths created by Pkgacct.
	if !strings.HasPrefix(path, "/home/cpmove-") || strings.Contains(path, "..") {
		return fmt.Errorf("RemoveRemote: refuses non-cpmove path %q", path)
	}
	cmd := fmt.Sprintf("rm -f '%s'", shellQuote(path))
	_, err := s.run(ctx, 30*time.Second, cmd)
	return err
}

func (d *Discoverer) Pkgacct(ctx context.Context, raw interface{}, account string) (string, error) {
	s, ok := raw.(*session)
	if !ok || s == nil {
		return "", errors.New("Pkgacct: bad session")
	}
	if s.principal != PrincipalAdmin {
		return "", errors.New("Pkgacct: requires admin/root principal on source")
	}
	if !looksLikeCpanelUsername(account) {
		return "", fmt.Errorf("Pkgacct: invalid account %q", account)
	}

	subctx, cancel := context.WithTimeout(ctx, PkgacctTimeout)
	defer cancel()

	// pkgacct emits chatty progress to stderr — discard it with
	// 2>/dev/null so the SSH session's stderr buffer doesn't grow
	// unbounded over the long run. stdout is the produced-path
	// summary line.
	cmd := fmt.Sprintf("/scripts/pkgacct '%s' 2>/dev/null", shellQuote(account))
	out, err := s.run(subctx, PkgacctTimeout, cmd)
	if err != nil {
		return "", fmt.Errorf("pkgacct: %w", err)
	}
	// Default tarball path. cPanel's pkgacct prints "compressed
	// successfully ..." with the path; for v1 we trust the
	// canonical location rather than parse stdout. Fall back to
	// stdout-grep if the canonical path is missing on the source.
	canonical := fmt.Sprintf("/home/cpmove-%s.tar.gz", account)
	if path := parsePkgacctOutput(out); path != "" {
		canonical = path
	}
	return canonical, nil
}

// parsePkgacctOutput scans pkgacct stdout for a `Compressed
// successfully /<path>` line and returns the absolute path.
// Empty string when the line isn't present.
func parsePkgacctOutput(stdout []byte) string {
	for _, line := range strings.Split(string(stdout), "\n") {
		if !strings.Contains(line, "ompressed successfully") {
			continue
		}
		fields := strings.Fields(line)
		for _, f := range fields {
			if strings.HasPrefix(f, "/") {
				return strings.TrimSpace(f)
			}
		}
	}
	return ""
}

// PullFile streams a remote file to a local writer via SSH exec
// `cat <path>`. Handles tarballs of any size; io.Copy + an SSH
// session pipe avoids buffering the whole file in memory.
func (d *Discoverer) PullFile(ctx context.Context, raw interface{}, remotePath, localPath string) (int64, error) {
	s, ok := raw.(*session)
	if !ok || s == nil {
		return 0, errors.New("PullFile: bad session")
	}
	if !filepath.IsAbs(remotePath) {
		return 0, fmt.Errorf("PullFile: remote path must be absolute, got %q", remotePath)
	}
	if !filepath.IsAbs(localPath) {
		return 0, fmt.Errorf("PullFile: local path must be absolute, got %q", localPath)
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o750); err != nil {
		return 0, fmt.Errorf("mkdir local: %w", err)
	}
	w, err := os.OpenFile(localPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, fmt.Errorf("open local: %w", err)
	}
	defer w.Close()

	sess, err := s.client.NewSession()
	if err != nil {
		return 0, fmt.Errorf("ssh new session: %w", err)
	}
	defer sess.Close()

	stdout, err := sess.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("ssh stdout pipe: %w", err)
	}

	cmd := fmt.Sprintf("cat '%s'", shellQuote(remotePath))
	if err := sess.Start(cmd); err != nil {
		return 0, fmt.Errorf("ssh start cat: %w", err)
	}

	// Copy under the caller's context — a cancelled job kills the
	// pipe, which propagates to the remote cat as SIGPIPE.
	done := make(chan error, 1)
	var copied int64
	go func() {
		n, err := io.Copy(w, stdout)
		copied = n
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			_ = sess.Signal(ssh.SIGKILL)
			return copied, fmt.Errorf("pull copy: %w", err)
		}
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		return copied, ctx.Err()
	}
	if err := sess.Wait(); err != nil {
		return copied, fmt.Errorf("ssh cat exit: %w", err)
	}
	return copied, nil
}

// shellQuote escapes a value for safe single-quote interpolation
// inside a /bin/sh command line. We only allow it on the account
// name + path arguments; any other operator-controlled string
// path through joinArgs (which panics on a quote in argv —
// tighter than this).
func shellQuote(s string) string {
	// Replace ' with '\''. Defensive — every caller already
	// validates against looksLikeCpanelUsername / IsAbs path.
	return strings.ReplaceAll(s, "'", `'\''`)
}

// looksLikeCpanelUsername — cPanel allows lower-case + digits +
// underscore, 1-16 chars. Tighter than POSIX (cPanel itself
// refuses uppercase + most punctuation at account creation).
func looksLikeCpanelUsername(s string) bool {
	if len(s) < 1 || len(s) > 16 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}
