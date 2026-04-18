package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// cronListParams represents the input to cron.list.
type cronListParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

// cronListResponse represents the output from cron.list.
type cronListResponse struct {
	UnitFiles []string `json:"unit_files"` // Job IDs (strip prefix/suffix from filenames)
}

// cronListHandler lists all cron job unit files for a user.
func cronListHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p cronListParams
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

	// Build the cron-units directory path
	unitsDir := filepath.Join("/etc/jabali-panel/cron-units", p.Username)

	// List all .timer files in the directory
	entries, err := os.ReadDir(unitsDir)
	if err != nil {
		// Directory may not exist yet (user has no jobs); return empty list
		if os.IsNotExist(err) {
			return &cronListResponse{UnitFiles: []string{}}, nil
		}
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to read cron-units directory: %v", err),
		}
	}

	var jobIDs []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Match pattern: jabali-cron-<id>.timer
		if strings.HasPrefix(name, "jabali-cron-") && strings.HasSuffix(name, ".timer") {
			// Extract job ID from "jabali-cron-<id>.timer"
			jobID := strings.TrimPrefix(name, "jabali-cron-")
			jobID = strings.TrimSuffix(jobID, ".timer")
			jobIDs = append(jobIDs, jobID)
		}
	}

	return &cronListResponse{UnitFiles: jobIDs}, nil
}

// init registers the cron.list command.
func init() {
	Default.Register("cron.list", cronListHandler)
}
