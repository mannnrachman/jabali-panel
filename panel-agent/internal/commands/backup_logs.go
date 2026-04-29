// backup.logs — return journalctl tail for a backup transient unit.
// Caller passes {job_id, kind}; we pick the right unit name and shell
// out to journalctl. ULID regex on job_id defends against shell metas
// even though we exec via os/exec (no shell).
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

var backupLogsJobIDRE = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

type backupLogsParams struct {
	JobID string `json:"job_id"`
	Kind  string `json:"kind"` // account_backup | system_backup | account_restore | system_restore
	// Lines is an optional tail size cap. Default 5000.
	Lines int `json:"lines,omitempty"`
}

type backupLogsResponse struct {
	Unit      string `json:"unit"`
	Status    string `json:"status"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	LogText   string `json:"log_text"`
	FetchedAt string `json:"fetched_at"`
}

func unitForBackupKind(kind, jobID string) (string, bool) {
	switch kind {
	case "account_backup", "account_restore":
		return fmt.Sprintf("jabali-backup-%s.service", jobID), true
	case "system_backup", "system_restore":
		return fmt.Sprintf("jabali-system-backup-%s.service", jobID), true
	default:
		return "", false
	}
}

func backupLogsHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var p backupLogsParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if !backupLogsJobIDRE.MatchString(p.JobID) {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "job_id must be a 26-char ULID"}
	}
	unit, ok := unitForBackupKind(p.Kind, p.JobID)
	if !ok {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "kind must be account_backup|account_restore|system_backup|system_restore"}
	}
	lines := p.Lines
	if lines <= 0 || lines > 20000 {
		lines = 5000
	}

	statusOut, _ := exec.CommandContext(ctx, "systemctl", "is-active", unit).Output()
	status := strings.TrimSpace(string(statusOut))

	journalArgs := []string{"-u", unit, "--no-pager", "-o", "cat", "-n", strconv.Itoa(lines)}
	journalOut, _ := exec.CommandContext(ctx, "journalctl", journalArgs...).Output()

	resp := backupLogsResponse{
		Unit:      unit,
		Status:    status,
		LogText:   string(journalOut),
		FetchedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if status == "inactive" || status == "failed" {
		showOut, err := exec.CommandContext(ctx, "systemctl", "show", unit,
			"--property=ExecMainStatus", "--value").Output()
		if err == nil {
			if v, perr := strconv.Atoi(strings.TrimSpace(string(showOut))); perr == nil {
				resp.ExitCode = &v
			}
		}
	}
	return resp, nil
}

func init() {
	Default.Register("backup.logs", backupLogsHandler)
}
