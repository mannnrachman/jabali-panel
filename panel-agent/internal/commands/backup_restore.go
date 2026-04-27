// Step 7 of M30: backup.restore agent command. Reads the manifest
// snapshot, resolves sibling stage snapshots by job-id, restores each
// in order. Stage failures are recorded; only fatal errors (lock
// contention, manifest unreadable) abort the whole run.
//
// Concurrency gate: a single global flock at
// /var/lib/jabali-backups/.restore.lock prevents parallel restores
// from racing nginx reload + PowerDNS NOTIFY + MariaDB DDL.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
)

const restoreLockPath = "/var/lib/jabali-backups/.restore.lock"

type backupRestoreParams struct {
	JobID              string `json:"job_id"`
	ManifestSnapshotID string `json:"manifest_snapshot_id"`
	TargetUserID       string `json:"target_user_id"`
	Overwrite          bool   `json:"overwrite"`
}

type backupRestoreResult struct {
	JobID  string                 `json:"job_id"`
	Stages []backupRestoreStage   `json:"stages"`
}

type backupRestoreStage struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// ErrRestoreLocked is the typed error returned when another restore is
// already holding the global flock.
var ErrRestoreLocked = errors.New("another restore is in progress")

func backupRestoreHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var req backupRestoreParams
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, bkInvalidArg("malformed JSON body")
	}
	if !ulidRE.MatchString(req.JobID) {
		return nil, bkInvalidArg("job_id must be a 26-char ULID")
	}
	if req.ManifestSnapshotID == "" {
		return nil, bkInvalidArg("manifest_snapshot_id required")
	}

	// Single global flock — held for the duration of the restore. Use
	// LOCK_NB so a busy host returns an error rather than blocking
	// the agent thread.
	if err := os.MkdirAll(filepath.Dir(restoreLockPath), 0o750); err != nil {
		return nil, bkInternal("mkdir lock dir", err)
	}
	lf, err := os.OpenFile(restoreLockPath, os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return nil, bkInternal("open lock file", err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, ErrRestoreLocked
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	c := backup.New(backup.DefaultConfig())

	// Step 1 — pull the manifest, validate schema, walk stages.
	manifestBytes, err := c.Dump(ctx, req.ManifestSnapshotID, "manifest.json")
	if err != nil {
		return nil, bkInternal("read manifest", err)
	}
	manifest, err := backup.AccountManifestFromBytes(manifestBytes)
	if err != nil {
		return nil, bkInternal("parse manifest", err)
	}

	out := backupRestoreResult{JobID: req.JobID}

	// Stage walk. Each Stages[i] in the manifest carries the snapshot
	// id we restore. Restore order matters in v2 (db before mail
	// because mailboxes link to db rows); v1 honors manifest order
	// and lets the operator re-run targeted stages by hand if needed.
	for _, st := range manifest.Stages {
		if st.SnapshotID == "" || st.Status != backup.StageStatusOK {
			out.Stages = append(out.Stages, backupRestoreStage{
				Name: st.Name, Status: backup.StageStatusSkipped,
			})
			continue
		}
		target := filepath.Join("/var/lib/jabali-backups/restore-staging",
			req.JobID, st.Name)
		if err := os.MkdirAll(target, 0o750); err != nil {
			out.Stages = append(out.Stages, backupRestoreStage{
				Name: st.Name, Status: backup.StageStatusFailed,
				Error: fmt.Sprintf("mkdir staging: %v", err),
			})
			continue
		}
		err := c.Restore(ctx, backup.RestoreOpts{
			SnapshotID: st.SnapshotID,
			Target:     target,
		})
		stageOut := backupRestoreStage{Name: st.Name, Status: backup.StageStatusOK}
		if err != nil {
			stageOut.Status = backup.StageStatusFailed
			stageOut.Error = err.Error()
		}
		out.Stages = append(out.Stages, stageOut)
	}
	return out, nil
}

// backupRestoreStatusHandler reports the materialized staging area for
// a restore job. v1 surface; v2 wires unit-status + import-progress.
func backupRestoreStatusHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var req struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, bkInvalidArg("malformed JSON body")
	}
	if !ulidRE.MatchString(req.JobID) {
		return nil, bkInvalidArg("job_id must be a 26-char ULID")
	}
	staging := filepath.Join("/var/lib/jabali-backups/restore-staging", req.JobID)
	entries, err := os.ReadDir(staging)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{"job_id": req.JobID, "stages_present": []string{}}, nil
		}
		return nil, bkInternal("read staging dir", err)
	}
	var stages []string
	for _, e := range entries {
		if e.IsDir() {
			stages = append(stages, e.Name())
		}
	}
	return map[string]any{
		"job_id":         req.JobID,
		"stages_present": stages,
		"updated_at":     time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// trim keeps log noise down on errors. (Kept here to avoid a circular
// import; matches the existing pattern in security_malware.go.)
func _trimRestore(s string) string {
	return strings.TrimSpace(s)
}

func init() {
	Default.Register("backup.restore", backupRestoreHandler)
	Default.Register("backup.restore_status", backupRestoreStatusHandler)
}
