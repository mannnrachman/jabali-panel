package cpanel

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
)

// HomeImportResult is returned to the restore-stage caller.
type HomeImportResult struct {
	BytesCopied int64    `json:"bytes_copied"`
	Files       int64    `json:"files"`
	DestPath    string   `json:"dest_path"`
	Skipped     []string `json:"skipped,omitempty"`
}

// ImportHome dispatches `migration.import_home` on the agent. Agent
// owns the rsync (privileged operation — needs root for chown -R
// and to write into /home/<target>/). Panel-api just provides the
// extracted homedir path + the destination jabali username.
//
// jobID lives in the manifest call so the agent can scope its
// staging path validation to the right migration.
func ImportHome(
	ctx context.Context,
	agentCaller agent.AgentInterface,
	parsed *ParsedTarball,
	jobID, destUser string,
) (*HomeImportResult, error) {
	if agentCaller == nil {
		return nil, fmt.Errorf("ImportHome: agent caller nil")
	}
	if parsed == nil {
		return nil, fmt.Errorf("ImportHome: parsed nil")
	}
	if parsed.HomeDir == "" {
		// No homedir in the tarball — empty cpmove or restricted
		// extraction. Not fatal: account migrations often have a
		// homedir even when otherwise minimal, but if it's missing
		// the caller might still want to run DB / mail / DNS
		// imports without home content.
		return &HomeImportResult{Skipped: []string{"no_homedir_in_tarball"}}, nil
	}
	if jobID == "" || destUser == "" {
		return nil, fmt.Errorf("ImportHome: jobID + destUser required")
	}

	raw, err := agentCaller.Call(ctx, "migration.import_home", map[string]any{
		"job_id":    jobID,
		"src_dir":   parsed.HomeDir,
		"dest_user": destUser,
	})
	if err != nil {
		return nil, fmt.Errorf("agent.migration.import_home: %w", err)
	}
	var resp HomeImportResult
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("ImportHome decode: %w", err)
	}
	return &resp, nil
}
