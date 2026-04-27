// Step 4 of M30: backup.databases agent command. Streams each
// `mariadb-dump` directly into a restic stdin snapshot tagged
// stage=db, db=<dbname>. Repo dedup means common WordPress schema
// shows up once on disk despite N user installs.
package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
)

type backupDatabasesParams struct {
	JobID     string   `json:"job_id"`
	UserID    string   `json:"user_id"`
	Username  string   `json:"username"`
	Databases []string `json:"databases"`
}

type backupDatabasesResult struct {
	Snapshots []backupDBStageSnapshot `json:"snapshots"`
}

type backupDBStageSnapshot struct {
	DB         string `json:"db"`
	SnapshotID string `json:"snapshot_id"`
	BytesAdded uint64 `json:"bytes_added"`
	BytesTotal uint64 `json:"bytes_total"`
	Error      string `json:"error,omitempty"`
}

func backupDatabasesHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var req backupDatabasesParams
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, bkInvalidArg("malformed JSON body")
	}
	if !ulidRE.MatchString(req.JobID) {
		return nil, bkInvalidArg("job_id must be a 26-char ULID")
	}
	if !ulidRE.MatchString(req.UserID) {
		return nil, bkInvalidArg("user_id must be a 26-char ULID")
	}
	if !backupUsernameRE.MatchString(req.Username) {
		return nil, bkInvalidArg("username must match ^[a-z][a-z0-9_-]{0,31}$")
	}
	if len(req.Databases) == 0 {
		return backupDatabasesResult{}, nil
	}
	for _, db := range req.Databases {
		if !dbNameRE.MatchString(db) {
			return nil, bkInvalidArg(fmt.Sprintf("database name invalid: %s", db))
		}
	}

	if _, err := bkResticBin(); err != nil {
		return nil, bkInternal("restic missing", err)
	}
	if _, err := exec.LookPath("mariadb-dump"); err != nil {
		return nil, bkInternal("mariadb-dump missing", err)
	}

	c := backup.New(backup.DefaultConfig())
	out := backupDatabasesResult{Snapshots: make([]backupDBStageSnapshot, 0, len(req.Databases))}
	for _, db := range req.Databases {
		snap, err := dumpOneDatabase(ctx, c, req.JobID, req.UserID, db)
		if err != nil {
			out.Snapshots = append(out.Snapshots, backupDBStageSnapshot{DB: db, Error: err.Error()})
			continue
		}
		out.Snapshots = append(out.Snapshots, *snap)
	}
	return out, nil
}

// dumpOneDatabase pipes mariadb-dump → restic backup --stdin. We avoid
// shelling out twice (no intermediate file) so the dump stays in tmpfs.
func dumpOneDatabase(ctx context.Context, c *backup.Client, jobID, userID, db string) (*backupDBStageSnapshot, error) {
	cmd := exec.CommandContext(ctx, "mariadb-dump",
		"--single-transaction", "--skip-lock-tables",
		"--routines", "--triggers", "--events",
		"--hex-blob",
		db,
	)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mariadb-dump: %w", err)
	}

	// Buffer the dump so restic --stdin sees it through the wrapper's
	// io.Reader. Tail end of the dump is required before the restic
	// invocation can complete its summary write — straight piping plus
	// goroutines is overkill for the typical dump size (KB-MB range).
	tags := backup.AccountBackupTags(jobID, userID, backup.StageDB)
	tags = append(tags, backup.MakeTag(backup.TagKeyDB, db))

	// Pump dump bytes through a pipe into the wrapper.
	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer pw.Close()
		_, _ = io.Copy(pw, stdoutPipe)
	}()

	summary, err := c.Backup(ctx, backup.BackupOpts{
		Stdin:     pr,
		StdinName: db + ".sql",
		Tags:      tags,
	})
	wg.Wait()
	waitErr := cmd.Wait()
	if waitErr != nil {
		return nil, fmt.Errorf("mariadb-dump %s: %w (stderr: %s)", db, waitErr, stderrBuf.String())
	}
	if err != nil {
		return nil, fmt.Errorf("restic backup --stdin: %w", err)
	}
	return &backupDBStageSnapshot{
		DB:         db,
		SnapshotID: summary.SnapshotID,
		BytesAdded: summary.DataAdded,
		BytesTotal: summary.TotalBytesProcessed,
	}, nil
}

func init() {
	Default.Register("backup.databases", backupDatabasesHandler)
}
