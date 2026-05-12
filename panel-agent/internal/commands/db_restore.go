package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// dbRestoreParams is the input shape for db.restore.
type dbRestoreParams struct {
	DBName string `json:"db_name"`
	Path   string `json:"path"`
	// ResetBeforeRestore — ADR-0095 amendment 2026-05-12. When true,
	// the handler issues DROP DATABASE IF EXISTS + CREATE DATABASE
	// before streaming the dump. Makes the restore idempotent under
	// retry-resume (the M35.1 default retry path). Migration importer
	// + retry-from-scratch set this; first-time restores against a
	// freshly-provisioned DB don't need it. Default false keeps
	// historic behaviour intact.
	ResetBeforeRestore bool `json:"reset_before_restore,omitempty"`
}

// dbRestoreResponse is the output shape for db.restore.
type dbRestoreResponse struct {
	OK bool `json:"ok"`
}

// dbRestoreNameRegex validates MariaDB database name format.
var dbRestoreNameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

func dbRestoreHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dbRestoreParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate db_name format.
	if !dbRestoreNameRegex.MatchString(p.DBName) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid database name",
		}
	}

	// Reject dangerous patterns (second layer of defense).
	if strings.Contains(p.DBName, "/") ||
		strings.Contains(p.DBName, "\\") ||
		strings.Contains(p.DBName, ";") ||
		strings.Contains(p.DBName, "\n") ||
		strings.Contains(p.DBName, "\r") ||
		strings.Contains(p.DBName, " ") ||
		strings.Contains(p.DBName, ".") {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "invalid database name",
		}
	}

	// Validate path is under an allowed restore directory.
	// /var/lib/jabali/restore/    — interactive restores
	// /var/lib/jabali-migrations/ — migration importer
	if !strings.HasPrefix(p.Path, "/var/lib/jabali/restore/") &&
		!strings.HasPrefix(p.Path, "/var/lib/jabali-migrations/") {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "restore path must be under /var/lib/jabali/restore/ or /var/lib/jabali-migrations/",
		}
	}

	// Ensure the file exists and is readable.
	f, err := os.Open(p.Path)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "restore file not found or not readable",
		}
	}
	defer f.Close()

	// If asked to reset, drop + recreate the DB FIRST so the dump
	// can stream into an empty schema. Idempotent: DROP IF EXISTS is
	// safe on a never-existed DB; CREATE always succeeds.
	if p.ResetBeforeRestore {
		resetSQL := fmt.Sprintf(
			"DROP DATABASE IF EXISTS `%s`; CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;",
			p.DBName, p.DBName,
		)
		reset := exec.CommandContext(ctx, "mysql", "-e", resetSQL)
		if out, err := reset.CombinedOutput(); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("reset failed: %v: %s", err, string(out)),
			}
		}
	}

	// Run mysql with stdin from the file, database name as positional argument.
	cmd := exec.CommandContext(ctx, "mysql", p.DBName)
	cmd.Stdin = f

	if err := cmd.Run(); err != nil {
		// Always delete the file, whether restore succeeds or fails.
		_ = os.Remove(p.Path)
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "failed to restore database",
		}
	}

	// Delete the file on success.
	if err := os.Remove(p.Path); err != nil {
		// Log but don't fail if cleanup fails; restore succeeded.
		// In production, admin might need to manually clean up.
	}

	return dbRestoreResponse{OK: true}, nil
}

func init() {
	Default.Register("db.restore", dbRestoreHandler)
}
