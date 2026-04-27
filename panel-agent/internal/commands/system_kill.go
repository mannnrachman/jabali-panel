// system_kill.go — agent handler for system.kill_process.
//
// Sends a signal to a pid by number. Default signal is SIGTERM; the
// caller can request SIGKILL via `force: true`. Refuses to touch a
// hardcoded denylist of system-critical pids (init/systemd, sshd,
// jabali-panel + jabali-agent themselves) so an admin slip in the UI
// can't lock the operator out of the host.
//
// Why a system-wide command and not service.stop: services have a
// systemd unit and live the unit lifecycle; arbitrary processes (e.g.
// a runaway PHP-FPM worker for a single user, or clamd eating RAM)
// don't. Operators in the wild kill -TERM by pid all the time; the
// Server Status page just exposes that workflow.

package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type systemKillParams struct {
	PID   int  `json:"pid"`
	Force bool `json:"force"`
}

type systemKillResponse struct {
	PID    int    `json:"pid"`
	Signal string `json:"signal"`
}

// killDenylistComm is the set of comm values we refuse to signal at
// all. PID 1 is also rejected unconditionally below regardless of
// comm. The list is conservative; expand as we learn what operators
// regret killing.
var killDenylistComm = map[string]bool{
	"systemd":      true,
	"init":         true,
	"sshd":         true,
	"jabali-panel": true,
	"jabali-agent": true,
}

func systemKillHandler(_ context.Context, raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p systemKillParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.PID < 2 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "pid must be >= 2"}
	}

	// Read /proc/<pid>/stat → comm to enforce the denylist. If the pid
	// is gone we report not_found rather than silently succeeding.
	comm, ok := readProcComm(p.PID)
	if !ok {
		return nil, &agentwire.AgentError{Code: agentwire.CodeNotFound, Message: fmt.Sprintf("pid %d does not exist", p.PID)}
	}
	if killDenylistComm[comm] {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodePermissionDenied,
			Message: fmt.Sprintf("pid %d (%s) is on the kill denylist", p.PID, comm),
		}
	}

	sig := syscall.SIGTERM
	sigName := "SIGTERM"
	if p.Force {
		sig = syscall.SIGKILL
		sigName = "SIGKILL"
	}
	if err := syscall.Kill(p.PID, sig); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("kill(%d, %s): %v", p.PID, sigName, err),
		}
	}
	return systemKillResponse{PID: p.PID, Signal: sigName}, nil
}

// readProcComm reads /proc/<pid>/stat and returns the comm field. ok
// is false when the pid has gone away mid-call. Re-implemented here
// (rather than imported from system_processes.go) to avoid coupling
// the kill path to the population-scan path.
func readProcComm(pid int) (string, bool) {
	raw, err := os.ReadFile(fmt.Sprintf("%s/%d/stat", procDir, pid))
	if err != nil {
		return "", false
	}
	open := strings.IndexByte(string(raw), '(')
	close := strings.LastIndexByte(string(raw), ')')
	if open < 0 || close < 0 || close <= open {
		return "", false
	}
	return string(raw)[open+1 : close], true
}

func init() {
	Default.Register("system.kill_process", systemKillHandler)
}
