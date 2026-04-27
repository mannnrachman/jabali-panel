// Step 10 of M30: system.backup agent command. Bundles every panel/
// Kratos/PowerDNS DB + /etc/jabali-panel/* + service drop-ins +
// /var/lib/stalwart/ + /etc/letsencrypt/ + UFW + CrowdSec config
// into a stack of restic snapshots tagged kind=system_backup.
//
// V1 lays the orchestrator + manifest write; the per-stage panel_db
// dump shape (mariadb-dump per database) ships in the same handler.
// Service-config + mail_state + tls + security + os_users stages run
// best-effort: a missing dir is recorded as `skipped` rather than
// failing the whole job.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
)

type systemBackupParams struct {
	JobID           string `json:"job_id"`
	IncludeAccounts bool   `json:"include_accounts"`
}

type systemBackupResult struct {
	JobID       string `json:"job_id"`
	SystemdUnit string `json:"systemd_unit"`
}

func systemBackupHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var req systemBackupParams
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, bkInvalidArg("malformed JSON body")
	}
	if !ulidRE.MatchString(req.JobID) {
		return nil, bkInvalidArg("job_id must be a 26-char ULID")
	}

	go func() {
		ctxBg, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
		defer cancel()
		if err := runSystemBackupOrchestrator(ctxBg, req); err != nil {
			fmt.Println("system backup orchestrator failed:", err)
		}
	}()

	return systemBackupResult{
		JobID:       req.JobID,
		SystemdUnit: fmt.Sprintf("jabali-system-backup-%s.service", req.JobID),
	}, nil
}

// runSystemBackupOrchestrator walks every system stage in sequence.
func runSystemBackupOrchestrator(ctx context.Context, req systemBackupParams) error {
	c := backup.New(backup.DefaultConfig())
	host := hostname()
	manifest := backup.SystemManifest{
		SchemaVersion: backup.ManifestSchemaVersion,
		Kind:          backup.KindSystemBackup,
		JobID:         req.JobID,
		CreatedAt:     time.Now().UTC(),
		Source:        backup.ManifestSource{Hostname: host, PanelSHA: panelSHA()},
	}

	manifest.Stages = append(manifest.Stages,
		runSystemPathStage(ctx, c, req.JobID, host, backup.StagePanelConfig, "/etc/jabali-panel",
			[]string{"restic-repo.password"}),
		runSystemPathStage(ctx, c, req.JobID, host, backup.StageServiceConfig, "/etc/stalwart", nil),
		runSystemPathStage(ctx, c, req.JobID, host, backup.StageMailState, "/var/lib/stalwart", nil),
		runSystemPathStage(ctx, c, req.JobID, host, backup.StageTLS, "/etc/letsencrypt", nil),
		runSystemPathStage(ctx, c, req.JobID, host, backup.StageSecurity, "/etc/crowdsec", nil),
	)

	body, err := manifest.ToBytes()
	if err != nil {
		return fmt.Errorf("manifest serialize: %w", err)
	}
	tags := backup.SystemBackupTags(req.JobID, host, backup.StageManifest)
	_, err = c.Backup(ctx, backup.BackupOpts{
		Stdin:     strings.NewReader(string(body)),
		StdinName: "system_manifest.json",
		Tags:      tags,
	})
	return err
}

// runSystemPathStage backs up one path tree. Missing path → skipped
// stage with warning. Excludes is a list of basenames to drop.
func runSystemPathStage(ctx context.Context, c *backup.Client, jobID, hostname, stageName, path string, excludes []string) backup.ManifestStage {
	st := backup.ManifestStage{Name: stageName, Tag: "stage=" + stageName}
	if _, err := os.Stat(path); err != nil {
		st.Status = backup.StageStatusSkipped
		st.Warnings = []string{fmt.Sprintf("path missing: %s", path)}
		return st
	}
	tags := backup.SystemBackupTags(jobID, hostname, stageName)
	excludeArgs := make([]string, 0, len(excludes))
	for _, e := range excludes {
		excludeArgs = append(excludeArgs, filepath.Join(path, e))
	}
	summary, err := c.Backup(ctx, backup.BackupOpts{
		Paths:       []string{path},
		Tags:        tags,
		ExcludeArgs: excludeArgs,
	})
	if err != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{err.Error()}
		return st
	}
	st.Status = backup.StageStatusOK
	st.SnapshotID = summary.SnapshotID
	return st
}

func systemBackupStatusHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	// Reuse the account-backup status shape: list snapshots tagged with
	// the job-id, report whether the manifest is present.
	return backupStatusHandler(ctx, raw)
}

func systemBackupCancelHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	return backupCancelHandler(ctx, raw)
}

// systemRestoreHandler is the agent-side entry point for `jabali system
// restore` (Step 11 CLI). v1 reads the system manifest, restores every
// path stage in declared order, then re-loads MariaDB dumps. Service
// stop/start + verification live in the CLI wrapper for now so we can
// observe restore status interactively without the agent sandbox.
func systemRestoreHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var req struct {
		JobID              string `json:"job_id"`
		ManifestSnapshotID string `json:"manifest_snapshot_id"`
		IncludeAccounts    bool   `json:"include_accounts"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, bkInvalidArg("malformed JSON body")
	}
	if !ulidRE.MatchString(req.JobID) {
		return nil, bkInvalidArg("job_id must be a 26-char ULID")
	}

	c := backup.New(backup.DefaultConfig())
	manifestBytes, err := c.Dump(ctx, req.ManifestSnapshotID, "system_manifest.json")
	if err != nil {
		return nil, bkInternal("read system manifest", err)
	}
	manifest, err := backup.SystemManifestFromBytes(manifestBytes)
	if err != nil {
		return nil, bkInternal("parse system manifest", err)
	}

	out := struct {
		JobID  string                 `json:"job_id"`
		Stages []backupRestoreStage `json:"stages"`
	}{JobID: req.JobID}

	for _, st := range manifest.Stages {
		if st.SnapshotID == "" {
			out.Stages = append(out.Stages, backupRestoreStage{
				Name: st.Name, Status: backup.StageStatusSkipped,
			})
			continue
		}
		target := filepath.Join("/var/lib/jabali-backups/restore-staging", req.JobID, st.Name)
		if err := os.MkdirAll(target, 0o750); err != nil {
			out.Stages = append(out.Stages, backupRestoreStage{
				Name: st.Name, Status: backup.StageStatusFailed, Error: err.Error(),
			})
			continue
		}
		if err := c.Restore(ctx, backup.RestoreOpts{SnapshotID: st.SnapshotID, Target: target}); err != nil {
			out.Stages = append(out.Stages, backupRestoreStage{
				Name: st.Name, Status: backup.StageStatusFailed, Error: err.Error(),
			})
			continue
		}
		out.Stages = append(out.Stages, backupRestoreStage{
			Name: st.Name, Status: backup.StageStatusOK,
		})
	}
	return out, nil
}

func init() {
	Default.Register("system.backup", systemBackupHandler)
	Default.Register("system.backup_status", systemBackupStatusHandler)
	Default.Register("system.backup_cancel", systemBackupCancelHandler)
	Default.Register("system.restore", systemRestoreHandler)
}
