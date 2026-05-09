// jabali-ssh-shell — every hosting user's login shell (M13).
//
// Reads /etc/jabali/ssh-sandbox-mode (single line: 'bubblewrap' or
// 'nspawn'), dispatches into the matching sandbox, and on any
// config error falls back to /usr/sbin/nologin. Never produces
// an unsandboxed bash. Failure mode = "user can't shell in";
// they SFTP instead.
//
// Plan invariants (plans/m13-ssh-shell-sandbox.md §0):
//   #1: Two modes only — bubblewrap (default) + nspawn (opt-in).
//       No 'none' / plain bash mode.
//   #2: Default mode = bubblewrap (lightweight, no rootfs cost).
//   #4: Fallback on bad config = nologin. Never bash.
//   #8: bwrap relies on its setuid bit; no sudo needed.
//   #9: Mode toggle is a single-file write.
//
// Step 1 ships the dispatch skeleton + nologin fallback. bwrap
// argv assembly + nspawn privilege bridge ship in Step 2 +
// Step 3 follow-ups; today's binary always lands in nologin
// because mode_dispatch returns an explicit "step-1-not-yet-
// wired" error so an operator running the wrapper before the
// sandbox bits ship gets a clear signal rather than silent
// passthrough.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	modeFile = "/etc/jabali/ssh-sandbox-mode"
	nologin  = "/usr/sbin/nologin"

	modeBubblewrap = "bubblewrap"
	modeNspawn     = "nspawn"
)

func main() {
	if err := run(); err != nil {
		// Best-effort surface a one-liner so SSH client sees a
		// clean rejection. nologin's own message lands when we
		// exec to it; this stderr line is for the cases where
		// nologin itself isn't found (defensive — every Debian/
		// Ubuntu host ships /usr/sbin/nologin).
		fmt.Fprintf(os.Stderr, "jabali-ssh-shell: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	mode, err := readMode()
	if err != nil {
		return execNologin(fmt.Sprintf("read mode: %v", err))
	}
	switch mode {
	case modeBubblewrap:
		return dispatchBubblewrap()
	case modeNspawn:
		return dispatchNspawn()
	default:
		return execNologin(fmt.Sprintf("unrecognised mode %q in %s (allowed: %s, %s)",
			mode, modeFile, modeBubblewrap, modeNspawn))
	}
}

// readMode pulls the single-line mode from /etc/jabali/ssh-sandbox-mode.
// Missing file → defaults to bubblewrap (plan §0 #2). Empty file or
// trim-only-whitespace → bubblewrap. Other content → returned
// verbatim for the dispatch switch to recognize / reject.
func readMode() (string, error) {
	raw, err := os.ReadFile(modeFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return modeBubblewrap, nil
		}
		return "", err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return modeBubblewrap, nil
	}
	return trimmed, nil
}

// dispatchBubblewrap will exec /usr/bin/bwrap with the per-user
// jail in Step 2. v1: explicit not-yet-wired error → nologin
// fallback. Operator running the wrapper before Step 2 ships
// sees a clear signal rather than silent passthrough.
func dispatchBubblewrap() error {
	if _, err := exec.LookPath("bwrap"); err != nil {
		return execNologin(fmt.Sprintf("bwrap not found in PATH (apt install bubblewrap): %v", err))
	}
	// TODO(M13 Step 2): build bwrap argv with per-user jail
	// (bind /home/<user> rw, /etc/passwd ro, /tmp tmpfs, etc.)
	// + exec the user's chosen interactive shell inside.
	return execNologin("M13 Step 1: bubblewrap dispatch not yet wired (Step 2 ships argv assembly)")
}

// dispatchNspawn will read /etc/jabali/users/<username>/nspawn-
// image (per-user image pin) and sudo into the M13-shipped
// jabali-nspawn-enter helper. Step 3 follow-up.
func dispatchNspawn() error {
	if _, err := exec.LookPath("systemd-nspawn"); err != nil {
		return execNologin(fmt.Sprintf("systemd-nspawn not found (apt install systemd-container): %v", err))
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return execNologin(fmt.Sprintf("sudo not found: %v", err))
	}
	username, err := currentUsername()
	if err != nil {
		return execNologin(fmt.Sprintf("resolve current user: %v", err))
	}
	pinPath := filepath.Join("/etc/jabali/users", username, "nspawn-image")
	pinRaw, err := os.ReadFile(pinPath)
	if err != nil {
		return execNologin(fmt.Sprintf("read image pin %s: %v", pinPath, err))
	}
	image := strings.TrimSpace(string(pinRaw))
	if image == "" {
		return execNologin(fmt.Sprintf("image pin %s is empty", pinPath))
	}
	// TODO(M13 Step 3): sudo /usr/local/bin/jabali-nspawn-enter
	// <image>; the helper validates image against the allowlist
	// scan of /var/lib/jabali-nspawn/images/ + exec's
	// systemd-nspawn --ephemeral --image=<path> --bind=/home/<u>.
	return execNologin("M13 Step 1: nspawn dispatch not yet wired (Step 3 ships sudo bridge)")
}

// currentUsername returns the SSH-side login name. Read from $USER
// first (set by sshd before exec'ing the login shell); falls back
// to looking up the real uid via /etc/passwd. Either path returns
// a clean string or the error.
func currentUsername() (string, error) {
	if u := os.Getenv("USER"); u != "" {
		return u, nil
	}
	uid := os.Getuid()
	pw, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(pw), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 3 {
			continue
		}
		if fields[2] == fmt.Sprintf("%d", uid) {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("uid %d not in /etc/passwd", uid)
}

// execNologin replaces the current process with /usr/sbin/nologin.
// The reason string is logged to stderr so an operator inspecting
// auth.log sees why the wrapper bailed.
//
// Returns nil on successful exec replacement (which doesn't return
// at all). Returns the underlying error when nologin can't be
// invoked (missing binary; rare). Caller's main() prints the
// fallback-of-fallback diagnostic.
func execNologin(reason string) error {
	if reason != "" {
		fmt.Fprintf(os.Stderr, "jabali-ssh-shell: %s — falling back to nologin\n", reason)
	}
	if _, err := os.Stat(nologin); err != nil {
		// Belt-and-braces — write 'no shell' to stdout so the
		// SSH client sees an explicit message even when the
		// system's nologin is broken.
		_, _ = io.WriteString(os.Stdout, "This account is not configured for shell access.\n")
		return fmt.Errorf("nologin missing at %s: %w", nologin, err)
	}
	return syscall.Exec(nologin, []string{nologin}, os.Environ())
}
