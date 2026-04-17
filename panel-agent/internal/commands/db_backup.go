package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// dbBackupParams is the input shape for db.backup.
type dbBackupParams struct {
	DBName string `json:"db_name"`
	Path   string `json:"path"`
}

// dbBackupResponse is the output shape for db.backup.
type dbBackupResponse struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

// dbBackupNameRegex validates MariaDB database name format.
var dbBackupNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func dbBackupHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dbBackupParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate db_name format.
	if !dbBackupNameRegex.MatchString(p.DBName) {
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

	backupPath := p.Path
	if backupPath == "" {
		// Generate default path with random hex string
		buf := make([]byte, 12)
		if _, err := rand.Read(buf); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: "failed to generate backup path",
			}
		}
		backupPath = fmt.Sprintf("/var/lib/jabali/backups/%s.sql", hex.EncodeToString(buf))
	}

	// Validate path is under /var/lib/jabali/backups/ and reject directory traversal
	if !strings.HasPrefix(backupPath, "/var/lib/jabali/backups/") {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "backup path must be under /var/lib/jabali/backups/",
		}
	}

	// Reject directory traversal attempts
	if strings.Contains(backupPath, "..") {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "backup path contains invalid characters",
		}
	}

	// Ensure directory exists with mode 0700
	backupDir := "/var/lib/jabali/backups"
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "failed to create backup directory",
		}
	}

	// Create the backup file
	f, err := os.Create(backupPath)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "failed to create backup file",
		}
	}
	defer f.Close()

	// Run mysqldump with the database name as a positional argument (no interpolation).
	cmd := exec.CommandContext(
		ctx,
		"mysqldump",
		"--single-transaction",
		"--quick",
		"--lock-tables=0",
		"--",
		p.DBName,
	)
	cmd.Stdout = f

	if err := cmd.Run(); err != nil {
		// Remove partial file on failure
		_ = os.Remove(backupPath)
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "failed to backup database",
		}
	}

	// chmod 0640 — 0600 would lock out panel-api (runs as jabali, while
	// the agent runs as root), which is the process that streams the
	// dump back to the HTTP client. The backup dir itself is 0700 and
	// already jabali-group-owned, so group-readable files leak only to
	// panel-api — exactly who needs them.
	if err := os.Chmod(backupPath, 0640); err != nil {
		_ = os.Remove(backupPath)
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "failed to set backup file permissions",
		}
	}

	// Get file size
	info, err := os.Stat(backupPath)
	if err != nil {
		_ = os.Remove(backupPath)
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "failed to stat backup file",
		}
	}

	return dbBackupResponse{
		Path:      backupPath,
		SizeBytes: info.Size(),
	}, nil
}

func init() {
	Default.Register("db.backup", dbBackupHandler)
}
