// M30.1 follow-up — agent-side test-connection for backup destinations.
// panel-api can't read the 0600 root:root creds env file or shell out
// with HOME=/root, so the full test runs here as root.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
)

type backupDestTestParams struct {
	URL              string   `json:"url"`
	CredentialsRef   string   `json:"credentials_ref,omitempty"`
	ExtraOptions     []string `json:"extra_options,omitempty"`
}

type backupDestTestResult struct {
	Status         string `json:"status"`
	StdoutPreview  string `json:"stdout_preview,omitempty"`
	Stderr         string `json:"stderr,omitempty"`
	Detail         string `json:"detail,omitempty"`
}

func backupDestTestHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var p backupDestTestParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid_arg: %w", err)
	}
	if p.URL == "" {
		return nil, fmt.Errorf("invalid_arg: url required")
	}
	var extraEnv []string
	if p.CredentialsRef != "" {
		env, err := backup.LoadEnvFile(p.CredentialsRef)
		if err != nil {
			return nil, fmt.Errorf("creds_load_failed: %w", err)
		}
		extraEnv = env
	}
	stdout, stderr, err := backup.SnapshotsRemote(
		ctx,
		nil,
		p.URL,
		backup.DefaultPasswordFile,
		extraEnv,
		p.ExtraOptions,
	)
	if err != nil {
		stderrStr := strings.TrimSpace(string(stderr))
		// "repository does not exist" / "unable to open config file"
		// means the SSH/SFTP layer succeeded — auth + reachability
		// are fine, just no restic repo at the path yet. The first
		// backup will run `restic init` and create it. From the
		// operator's POV the destination IS reachable, so report ok
		// with a hint.
		lower := strings.ToLower(stderrStr)
		if strings.Contains(lower, "repository does not exist") ||
			strings.Contains(lower, "unable to open config file") {
			return backupDestTestResult{
				Status:        "ok",
				StdoutPreview: "reachable — restic repo not yet initialized; first backup will create it",
			}, nil
		}
		return backupDestTestResult{
			Status: "error",
			Detail: err.Error(),
			Stderr: stderrStr,
		}, nil
	}
	return backupDestTestResult{
		Status:        "ok",
		StdoutPreview: firstNonEmptyLine(string(stdout)),
	}, nil
}

func firstNonEmptyLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if t != "" {
			return t
		}
	}
	return ""
}

func init() {
	Default.Register("backup.dest.test", backupDestTestHandler)
}
