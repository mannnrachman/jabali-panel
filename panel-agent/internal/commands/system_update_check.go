package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// systemUpdateCheckResponse is the wire shape for system.update_check.
// "behind_count" = git rev-list --count HEAD..origin/main, so 0 means
// up-to-date, N>0 means there are N commits to pull.
type systemUpdateCheckResponse struct {
	CurrentSHA  string `json:"current_sha"`
	RemoteSHA   string `json:"remote_sha"`
	BehindCount int    `json:"behind_count"`
	Branch      string `json:"branch"`
}

const (
	systemRepoDir     = "/opt/jabali-panel"
	systemServiceUser = "jabali"
)

// runAsServiceUser shells out via `sudo -u jabali` because /opt/jabali-panel
// is owned by the unprivileged service user; git 2.35+ refuses to operate
// on a repo owned by a different uid.
func runAsServiceUser(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"-u", systemServiceUser, "-H"}, args...)
	c := exec.CommandContext(ctx, "sudo", full...)
	out, err := c.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		return out, fmt.Errorf("%s: %w (stderr: %s)", strings.Join(args, " "), err, stderr)
	}
	return out, nil
}

func systemUpdateCheckHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	current, err := runAsServiceUser(ctx, "git", "-C", systemRepoDir, "rev-parse", "HEAD")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if _, err := runAsServiceUser(ctx, "git", "-C", systemRepoDir, "fetch", "--quiet", "origin", "main"); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	remote, err := runAsServiceUser(ctx, "git", "-C", systemRepoDir, "rev-parse", "origin/main")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	behind, err := runAsServiceUser(ctx, "git", "-C", systemRepoDir, "rev-list", "--count", "HEAD..origin/main")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	branchOut, _ := runAsServiceUser(ctx, "git", "-C", systemRepoDir, "rev-parse", "--abbrev-ref", "HEAD")

	return systemUpdateCheckResponse{
		CurrentSHA:  strings.TrimSpace(string(current)),
		RemoteSHA:   strings.TrimSpace(string(remote)),
		BehindCount: parseIntSafe(strings.TrimSpace(string(behind))),
		Branch:      strings.TrimSpace(string(branchOut)),
	}, nil
}

func parseIntSafe(s string) int {
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func init() {
	Default.Register("system.update_check", systemUpdateCheckHandler)
}
