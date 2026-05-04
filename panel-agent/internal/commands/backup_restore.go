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
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
)

const restoreLockPath = "/var/lib/jabali-backups/.restore.lock"

type backupRestoreParams struct {
	JobID              string   `json:"job_id"`
	ManifestSnapshotID string   `json:"manifest_snapshot_id"`
	TargetUserID       string   `json:"target_user_id"`
	// TargetUsername is the system account name (matches /etc/passwd).
	// Required for the apply step to chown home + scope mariadb loads.
	// API resolves this from the panel users repo before dispatching.
	TargetUsername string   `json:"target_username,omitempty"`
	Overwrite      bool     `json:"overwrite"`
	RepoURL        string   `json:"repo_url,omitempty"`
	CredentialsRef string   `json:"credentials_ref,omitempty"`
	ExtraOptions   []string `json:"extra_options,omitempty"`
	// ApplyStaged: when false the handler stops after materializing
	// stages into /var/lib/jabali-backups/restore-staging/<job_id>/
	// (recon mode). Default true → home+db are applied onto the live
	// system before the call returns.
	ApplyStaged *bool `json:"apply_staged,omitempty"`
}

type backupRestoreResult struct {
	JobID    string               `json:"job_id"`
	Stages   []backupRestoreStage `json:"stages"`
	Applied  []string             `json:"applied,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
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

	cfg, cerr := bkResticConfig(req.RepoURL, req.CredentialsRef, req.ExtraOptions)
	if cerr != nil {
		return nil, bkInternal("restic config", cerr)
	}
	c := backup.New(cfg)

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

	apply := true
	if req.ApplyStaged != nil {
		apply = *req.ApplyStaged
	}
	if apply {
		stagingRoot := filepath.Join("/var/lib/jabali-backups/restore-staging", req.JobID)
		applied, warnings := applyAccountRestore(ctx, stagingRoot, req.TargetUsername, manifest.Stages, out.Stages)
		out.Applied = applied
		out.Warnings = warnings
	} else {
		out.Warnings = append(out.Warnings,
			"apply_staged=false — files materialized to "+
				filepath.Join("/var/lib/jabali-backups/restore-staging", req.JobID)+
				"; nothing applied to live system")
	}
	return out, nil
}

// applyAccountRestore walks the materialized stage tree and applies
// home + db onto the live system. Order:
//
//   1. home → rsync staging/home/<username>/ → /home/<username>/
//      then chown -R <uid>:<gid> /home/<username>
//   2. db → for each stage with Items=[<dbname>]: mariadb <dbname>
//      < staging/db/<dbname>.sql (CREATE/DROP TABLE in dump rebuild
//      schema; existing GRANTs survive because the database row stays)
//
// mail is intentionally NOT auto-applied: stalwart-cli apply over a
// running spool corrupts RocksDB, plan.json has cross-host references
// (Domain/Tenant/Role) that need re-resolution, and bodies.tar untar
// risks lost messages. Operator gets a warning with the staging path
// and applies it manually.
//
// stageResults carries the per-stage materialization outcome from the
// caller; we only apply stages that materialized OK.
func applyAccountRestore(
	ctx context.Context,
	stagingRoot, username string,
	manifestStages []backup.ManifestStage,
	stageResults []backupRestoreStage,
) ([]string, []string) {
	statusOf := map[string]string{}
	for _, sr := range stageResults {
		statusOf[sr.Name] = sr.Status
	}
	var applied, warnings []string
	if username == "" {
		warnings = append(warnings,
			"apply skipped: target_username missing from request — pass target_username from API")
		return applied, warnings
	}
	u, uerr := user.Lookup(username)
	if uerr != nil {
		warnings = append(warnings,
			fmt.Sprintf("apply skipped: user.Lookup(%q): %v — does the account exist on this host?", username, uerr))
		return applied, warnings
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)

	for _, st := range manifestStages {
		if statusOf[st.Name] != backup.StageStatusOK {
			continue
		}
		switch st.Name {
		case backup.StageHome:
			// Restic preserves the absolute path; staged tree is at
			// stagingRoot/home/home/<username>/. Source needs a
			// trailing slash so rsync copies CONTENTS not the dir.
			src := filepath.Join(stagingRoot, "home", "home", username) + "/"
			dst := "/home/" + username + "/"
			if _, err := os.Stat(filepath.Clean(src)); err != nil {
				warnings = append(warnings,
					fmt.Sprintf("home: source %s missing: %v", src, err))
				continue
			}
			if err := exec.CommandContext(ctx, "rsync", "-aHAX", "--delete", src, dst).Run(); err != nil {
				warnings = append(warnings, fmt.Sprintf("home: rsync: %v", err))
				continue
			}
			if err := chownTreeRecursive(dst, uid, gid); err != nil {
				warnings = append(warnings, fmt.Sprintf("home: chown: %v", err))
				continue
			}
			applied = append(applied, fmt.Sprintf("home → /home/%s", username))

		case backup.StageDB:
			if len(st.Items) == 0 {
				warnings = append(warnings, "db: manifest stage missing items[0] db name")
				continue
			}
			db := st.Items[0]
			candidates := []string{
				filepath.Join(stagingRoot, "db", db+".sql"),
				filepath.Join(stagingRoot, "db", "stdin"),
			}
			var src string
			for _, p := range candidates {
				if _, err := os.Stat(p); err == nil {
					src = p
					break
				}
			}
			if src == "" {
				warnings = append(warnings, fmt.Sprintf("db %s: dump file not found in staging", db))
				continue
			}
			f, err := os.Open(src)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("db %s: open dump: %v", db, err))
				continue
			}
			cmd := exec.CommandContext(ctx, "mariadb", db)
			cmd.Stdin = f
			loadOut, lerr := cmd.CombinedOutput()
			_ = f.Close()
			if lerr != nil {
				warnings = append(warnings,
					fmt.Sprintf("db %s: mariadb load: %v: %s", db, lerr, strings.TrimSpace(string(loadOut))))
				continue
			}
			applied = append(applied, fmt.Sprintf("db → %s", db))

		case backup.StageMail:
			warnings = append(warnings,
				fmt.Sprintf("mail staged at %s — apply manually via 'stalwart-cli apply' (auto-apply unsafe over running spool)",
					filepath.Join(stagingRoot, "mail")))

		case backup.StageMeta, backup.StageDNS, backup.StageCron, backup.StageSSH,
			backup.StageApps, backup.StagePHP:
			// Metadata-only stages — informational, no apply.
		}
	}
	return applied, warnings
}

// chownTreeRecursive walks `root` and chowns every entry to uid:gid.
// We avoid `chown -R` because it forks for symlinks and tries to
// follow them; filepath.Walk + Lchown stays inside the tree.
func chownTreeRecursive(root string, uid, gid int) error {
	return filepath.Walk(root, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, gid)
	})
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
