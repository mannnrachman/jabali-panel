package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// userSliceUsernameRegex validates the strict username format: ^[a-z][a-z0-9_-]{0,31}$
// (no leading underscore, unlike the general POSIX regex)
var userSliceUsernameRegex = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// userSliceEnsureParams is the input shape for user.slice.ensure.
type userSliceEnsureParams struct {
	Username string `json:"username"`
}

// userSliceEnsureResponse is the output shape for user.slice.ensure.
type userSliceEnsureResponse struct {
	Username      string `json:"username"`
	SliceUnitPath string `json:"slice_unit_path"`
	FPMDropinPath string `json:"fpm_dropin_path"`
	LoginDropinPath string `json:"login_dropin_path"`
	UID           int    `json:"uid"`
	NoChange      bool   `json:"no_change,omitempty"`
}

// testMutex protects runCmd and systemdRoot variables for test isolation.
var testMutex sync.Mutex

// runCmd can be overridden in tests. In production, it dispatches to the OS.
var runCmd = func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// systemdRoot can be overridden in tests via JABALI_SYSTEMD_ROOT env var.
var systemdRoot = func() string {
	if root := os.Getenv("JABALI_SYSTEMD_ROOT"); root != "" {
		return root
	}
	return "/etc/systemd/system"
}

func userSliceEnsureHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p userSliceEnsureParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate username format: ^[a-z][a-z0-9_-]{0,31}$
	// (note: slightly stricter than usernameRegex which allows leading _)
	if !userSliceUsernameRegex.MatchString(p.Username) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid username %q: must match ^[a-z][a-z0-9_-]{0,31}$", p.Username),
		}
	}

	// Resolve uid via id -u
	testMutex.Lock()
	runCmdFn := runCmd
	testMutex.Unlock()

	stdout, _, err := runCmdFn(ctx, "id", "-u", p.Username)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeNotFound,
			Message: fmt.Sprintf("user %s does not exist on the host", p.Username),
		}
	}

	uid, err := strconv.Atoi(strings.TrimSpace(string(stdout)))
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to parse uid: %v", err),
		}
	}

	// Ensure the user is in www-data group so FPM can chown the socket
	// listen.group=www-data. Idempotent: usermod -aG on a member is a no-op.
	// Skip errors: a failure here leaves the socket chown failing loudly in FPM logs,
	// which is more diagnosable than silently wedging the reconcile.
	if _, _, gErr := runCmdFn(ctx, "usermod", "-aG", "www-data", p.Username); gErr != nil {
		// Log via stderr by returning a warning-wrapped error would be overkill;
		// socket chown will report the real problem.
		_ = gErr
	}

	testMutex.Lock()
	systemdRootFn := systemdRoot
	testMutex.Unlock()
	root := systemdRootFn()

	// Compute paths
	sliceUnitPath := filepath.Join(root, fmt.Sprintf("jabali-user-%s.slice", p.Username))
	fpmDropinDir := filepath.Join(root, fmt.Sprintf("jabali-fpm@%s.service.d", p.Username))
	fpmDropinPath := filepath.Join(fpmDropinDir, "slice.conf")
	loginDropinDir := filepath.Join(root, fmt.Sprintf("user@%d.service.d", uid))
	loginDropinPath := filepath.Join(loginDropinDir, "jabali.conf")

	// Build expected content
	sliceContent := buildSliceUnitContent(p.Username)
	fpmDropinContent := buildFPMDropinContent(p.Username)
	loginDropinContent := buildLoginDropinContent(p.Username)

	// Check if all files exist with expected content
	if filesMatch(sliceUnitPath, []byte(sliceContent)) &&
		filesMatch(fpmDropinPath, []byte(fpmDropinContent)) &&
		filesMatch(loginDropinPath, []byte(loginDropinContent)) {
		// All files match; no changes needed
		return &userSliceEnsureResponse{
			Username:        p.Username,
			SliceUnitPath:   sliceUnitPath,
			FPMDropinPath:   fpmDropinPath,
			LoginDropinPath: loginDropinPath,
			UID:             uid,
			NoChange:        true,
		}, nil
	}

	// Write slice unit
	if err := writeFileAtomically(sliceUnitPath, []byte(sliceContent), 0644); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to write slice unit: %v", err),
		}
	}

	// Write FPM dropin
	if err := os.MkdirAll(fpmDropinDir, 0755); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to create FPM dropin directory: %v", err),
		}
	}
	if err := writeFileAtomically(fpmDropinPath, []byte(fpmDropinContent), 0644); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to write FPM dropin: %v", err),
		}
	}

	// Write login dropin
	if err := os.MkdirAll(loginDropinDir, 0755); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to create login dropin directory: %v", err),
		}
	}
	if err := writeFileAtomically(loginDropinPath, []byte(loginDropinContent), 0644); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to write login dropin: %v", err),
		}
	}

	// Reload systemd
	testMutex.Lock()
	runCmdReload := runCmd
	testMutex.Unlock()

	_, _, err = runCmdReload(ctx, "systemctl", "daemon-reload")
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to reload systemd daemon: %v", err),
		}
	}

	// enable-linger so the user manager persists across logouts. Without
	// this, the user@<uid>.service.d/jabali.conf drop-in never takes
	// effect for users who have not logged in since last boot, which
	// means their shell sessions escape the slice hierarchy. This is the
	// "capture processes that systemd starts on behalf of the user"
	// piece of step 7.
	if _, stderr, lErr := runCmdReload(ctx, "loginctl", "enable-linger", p.Username); lErr != nil {
		// F10: treat "already enabled" stderr as success. loginctl's
		// exact message varies by systemd version but always contains
		// the word "already" for this condition.
		if !strings.Contains(string(stderr), "already") {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to enable-linger for %s: %v (%s)", p.Username, lErr, strings.TrimSpace(string(stderr))),
			}
		}
	}

	return &userSliceEnsureResponse{
		Username:        p.Username,
		SliceUnitPath:   sliceUnitPath,
		FPMDropinPath:   fpmDropinPath,
		LoginDropinPath: loginDropinPath,
		UID:             uid,
	}, nil
}

func buildSliceUnitContent(username string) string {
	return fmt.Sprintf(`[Unit]
Description=Jabali hosted user %s
Before=slices.target

[Slice]
CPUAccounting=yes
MemoryAccounting=yes
TasksAccounting=yes
`, username)
}

func buildFPMDropinContent(username string) string {
	return fmt.Sprintf(`[Service]
Slice=jabali-user-%s.slice
User=%s
Group=%s
`, username, username, username)
}

func buildLoginDropinContent(username string) string {
	return fmt.Sprintf(`[Service]
Slice=jabali-user-%s.slice
`, username)
}

// filesMatch checks if a file exists and its content matches expected.
func filesMatch(path string, expected []byte) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Equal(content, expected)
}

// writeFileAtomically writes to a temp file and renames it atomically.
func writeFileAtomically(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

func init() {
	Default.Register("user.slice.ensure", userSliceEnsureHandler)
}
