package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// systemUnitStatusParams is shared by system.update_status + system.apt_status.
// The same handler reads any allowlisted transient unit; locking the unit
// name to a server-side allowlist defends against a compromised panel-api
// asking for arbitrary unit logs.
type systemUnitStatusParams struct {
	Since string `json:"since,omitempty"`
}

type systemUnitStatusResponse struct {
	Unit       string `json:"unit"`
	Status     string `json:"status"`            // "active" | "inactive" | "failed" | "activating"
	ExitCode   *int   `json:"exit_code,omitempty"`
	LogTail    string `json:"log_tail"`
	FetchedAt  string `json:"fetched_at"`
}

// allowedStatusUnits is the explicit allowlist of transient units a caller
// can ask about. Hard-code so a malformed param can't tail an unrelated
// service like jabali-panel.
var allowedStatusUnits = map[string]string{
	"system.update_status": "jabali-update-oneshot.service",
	"system.apt_status":    "jabali-apt-oneshot.service",
}

func systemUnitStatusFor(unit string) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p systemUnitStatusParams
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
			}
		}
		// Default since=15m if not provided so we always get useful tail.
		since := p.Since
		if since == "" {
			since = time.Now().Add(-15 * time.Minute).UTC().Format(time.RFC3339)
		}
		statusOut, _ := exec.CommandContext(ctx, "systemctl", "is-active", unit).Output()
		status := strings.TrimSpace(string(statusOut))

		journalArgs := []string{"-u", unit, "--since=" + since, "--no-pager", "-o", "cat"}
		journalOut, _ := exec.CommandContext(ctx, "journalctl", journalArgs...).Output()

		resp := systemUnitStatusResponse{
			Unit:      unit,
			Status:    status,
			LogTail:   string(journalOut),
			FetchedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}

		// Read exit code from `systemctl show` once the unit terminates.
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
}

func init() {
	for cmdName, unit := range allowedStatusUnits {
		Default.Register(cmdName, systemUnitStatusFor(unit))
	}
}
