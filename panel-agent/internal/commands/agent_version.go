package commands

import (
	"context"
	"encoding/json"
	"runtime"
	"time"
)

// Build-time metadata. main.go sets Version via -ldflags before the init
// chain runs, so this file reads whatever's current at registration time.
// StartTime is captured when the process boots, in main.go, for the same
// reason. Keeping these as package-level vars avoids plumbing them through
// every handler when we only need them here.
var (
	Version   = "dev"
	StartTime = time.Now()
)

// agentVersionResponse is the canonical shape for agent.version. Callers
// use it as a liveness probe and an upgrade sanity check ("is the agent we
// thought we had the one actually running?").
type agentVersionResponse struct {
	Version       string `json:"version"`
	GoVersion     string `json:"go_version"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	StartedAt     string `json:"started_at"`
}

func agentVersionHandler(_ context.Context, _ json.RawMessage) (any, error) {
	now := time.Now()
	return agentVersionResponse{
		Version:       Version,
		GoVersion:     runtime.Version(),
		UptimeSeconds: int64(now.Sub(StartTime).Seconds()),
		StartedAt:     StartTime.UTC().Format(time.RFC3339),
	}, nil
}

func init() {
	Default.Register("agent.version", agentVersionHandler)
}
