package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/cronvalidate"
)

// cronApplyParams is the input for cron.apply command.
type cronApplyParams struct {
	UserID         string   `json:"user_id"`
	Username       string   `json:"username"`
	JobID          string   `json:"job_id"`
	Name           string   `json:"name"`
	Command        string   `json:"command"`
	Schedule       string   `json:"schedule"`
	OwnedDocroots  []string `json:"owned_docroots"`
}

// cronApplyResponse is the output from cron.apply.
type cronApplyResponse struct {
	ServicePath string `json:"service_path"`
	TimerPath   string `json:"timer_path"`
	NoChange    bool   `json:"no_change,omitempty"`
}

func cronApplyHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p cronApplyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate inputs
	if p.Username == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "username required",
		}
	}
	if p.JobID == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "job_id required",
		}
	}

	// SECURITY: Validate cron name to prevent control character injection (defense-in-depth)
	if err := cronvalidate.ValidateCronName(p.Name); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("name validation failed: %v", err),
		}
	}

	if p.Command == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "command required",
		}
	}
	if p.Schedule == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "schedule required",
		}
	}

	// Re-validate command and schedule (defense-in-depth per spec §3 invariant)
	cmd, err := cronvalidate.ValidateCommand(p.Command, p.OwnedDocroots)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("command validation failed: %v", err),
		}
	}

	if err := cronvalidate.ValidateSchedule(p.Schedule); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("schedule validation failed: %v", err),
		}
	}

	// Resolve user's UID
	u, err := user.Lookup(p.Username)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeNotFound,
			Message: fmt.Sprintf("user %s not found: %v", p.Username, err),
		}
	}

	uid, _ := strconv.Atoi(u.Uid)
	runtimeDir := fmt.Sprintf("/run/user/%d", uid)

	// Check user has linger enabled (ADR-0025 guarantee, but validate anyway)
	if err := checkUserLinger(ctx, p.Username); err != nil {
		return nil, &agentwire.AgentError{
			Code:    "user_not_lingering",
			Message: fmt.Sprintf("user %s does not have lingering enabled. Enable with: loginctl enable-linger %s", p.Username, p.Username),
		}
	}

	// Create cron-units directory
	unitsDir := fmt.Sprintf("/etc/jabali-panel/cron-units/%s", p.Username)
	if err := os.MkdirAll(unitsDir, 0755); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to create units directory: %v", err),
		}
	}

	// Generate unit file paths
	servicePath := filepath.Join(unitsDir, fmt.Sprintf("jabali-cron-%s.service", p.JobID))
	timerPath := filepath.Join(unitsDir, fmt.Sprintf("jabali-cron-%s.timer", p.JobID))

	// Generate unit file content
	serviceContent := buildCronServiceContent(p.JobID, p.Name, cmd, p.Username, p.OwnedDocroots)
	timerContent := buildCronTimerContent(p.JobID, p.Schedule)

	// Check for no-change (idempotency per spec §3 invariant)
	serviceMatch := filesMatch(servicePath, []byte(serviceContent))
	timerMatch := filesMatch(timerPath, []byte(timerContent))
	if serviceMatch && timerMatch {
		return &cronApplyResponse{
			ServicePath: servicePath,
			TimerPath:   timerPath,
			NoChange:    true,
		}, nil
	}

	// Write unit files atomically
	if err := writeFileAtomically(servicePath, []byte(serviceContent), 0644); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to write service file: %v", err),
		}
	}

	if err := writeFileAtomically(timerPath, []byte(timerContent), 0644); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to write timer file: %v", err),
		}
	}

	// Reload systemd as user and enable timer
	if err := systemctlUserExec(ctx, p.Username, runtimeDir, "daemon-reload"); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to reload systemd: %v", err),
		}
	}

	if err := systemctlUserExec(ctx, p.Username, runtimeDir, "enable", "--now", fmt.Sprintf("jabali-cron-%s.timer", p.JobID)); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to enable timer: %v", err),
		}
	}

	return &cronApplyResponse{
		ServicePath: servicePath,
		TimerPath:   timerPath,
	}, nil
}

// buildCronServiceContent generates the systemd service unit content.
func buildCronServiceContent(jobID, name string, cmd *cronvalidate.Command, username string, ownedDocroots []string) string {
	// Build ExecStart with single-quoted tokens (systemd parses whitespace to argv)
	execStart := "ExecStart="
	for i, token := range cmd.Argv {
		if i > 0 {
			execStart += " "
		}
		execStart += singleQuote(token)
	}

	// For ExecStartPre, determine the docroot to validate
	// We use the first argument that starts with / and is in an owned docroot
	docroot := ""
	for _, arg := range cmd.Argv[1:] {
		if strings.HasPrefix(arg, "/") {
			// Check if it's in an owned docroot
			for _, od := range ownedDocroots {
				if arg == od || strings.HasPrefix(arg, od+"/") {
					docroot = od
					break
				}
			}
			if docroot != "" {
				break
			}
		}
	}

	// If we have a docroot, add ExecStartPre validation
	execStartPre := ""
	if docroot != "" {
		execStartPre = fmt.Sprintf("ExecStartPre=/usr/local/libexec/jabali/cron-precheck %s\n", singleQuote(docroot))
	}

	return fmt.Sprintf(`[Unit]
Description=Jabali cron job %s (%s)
After=default.target
StartLimitIntervalSec=1
StartLimitBurst=1

[Service]
Type=oneshot
RemainAfterExit=no
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin
WorkingDirectory=%%h
%s%s
`, jobID, name, execStartPre, execStart)
}

// buildCronTimerContent generates the systemd timer unit content.
func buildCronTimerContent(jobID, schedule string) string {
	return fmt.Sprintf(`[Unit]
Description=Jabali cron timer for %s

[Timer]
OnCalendar=%s
Persistent=true
Unit=jabali-cron-%s.service

[Install]
WantedBy=timers.target
`, jobID, schedule, jobID)
}

// singleQuote wraps a token in single quotes, escaping any embedded single quotes.
// This matches systemd's ExecStart argv parsing where single quotes prevent shell interpretation.
func singleQuote(s string) string {
	// Single-quote escape: 'foo'\''bar' produces foo'bar when shell-parsed
	// But systemd doesn't shell-parse; it uses the raw token.
	// For systemd ExecStart, we just wrap in single quotes.
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// checkUserLinger verifies the user has loginctl enable-linger set.
func checkUserLinger(ctx context.Context, username string) error {
	// Check if user's linger session exists at /var/lib/systemd/linger/<username>
	lingerPath := filepath.Join("/var/lib/systemd/linger", username)
	_, err := os.Stat(lingerPath)
	return err
}

// systemctlUserExec runs a systemctl command as the specified user.
func systemctlUserExec(ctx context.Context, username string, runtimeDir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "sudo", "-u", username)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("XDG_RUNTIME_DIR=%s", runtimeDir),
	)
	cmd.Args = append(cmd.Args, "systemctl", "--user")
	cmd.Args = append(cmd.Args, args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func init() {
	Default.Register("cron.apply", cronApplyHandler)
}
