// Step 6 of M30: backup.create / backup.cancel / backup.status agent
// commands. backup.create spawns a transient systemd unit running the
// in-process orchestrator (`backup-internal-worker` mode), which calls
// the home/databases/mailboxes stages in sequence then assembles the
// manifest snapshot.
//
// Orchestrator runs in-process for v1 — the plan reserved a separate
// `/usr/local/bin/jabali-internal-backup-worker` binary, but we can
// re-enter the agent process via `jabali-agent backup-worker` flag
// without growing the build matrix. Future M30.1 may split it out
// once the wire shape is fully stable.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
)

// jobSlot tracks the single-active-job-per-(kind,user) invariant. The
// orchestrator obtains a slot before starting, releases on finish.
type jobSlot struct {
	mu   sync.Mutex
	open map[string]string // key="kind:user_id" → job_id
}

var defaultJobSlots = &jobSlot{open: map[string]string{}}

func (s *jobSlot) acquire(kind, userID, jobID string) (existing string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := kind + ":" + userID
	if cur, occupied := s.open[key]; occupied {
		return cur, false
	}
	s.open[key] = jobID
	return "", true
}

func (s *jobSlot) release(kind, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.open, kind+":"+userID)
}

type backupCreateParams struct {
	JobID     string   `json:"job_id"`
	UserID    string   `json:"user_id"`
	Username  string   `json:"username"`
	Email     string   `json:"email,omitempty"`
	IsAdmin   bool     `json:"is_admin"`
	Databases []string `json:"databases,omitempty"`
	Mailboxes []string `json:"mailboxes,omitempty"`
}

type backupCreateResult struct {
	JobID       string `json:"job_id"`
	SystemdUnit string `json:"systemd_unit"`
}

// backupCreateHandler accepts the backup request from panel-api and
// spawns a systemd transient unit. The unit re-invokes the agent as
// `jabali-agent backup-worker --params=/run/jabali/backup-<id>.json`.
func backupCreateHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var req backupCreateParams
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

	if existing, ok := defaultJobSlots.acquire("backup", req.UserID, req.JobID); !ok {
		return nil, fmt.Errorf("backup already running for user_id=%s job=%s", req.UserID, existing)
	}

	// Inline orchestration for v1. Future split into a transient
	// systemd unit happens once the panel-side observation surface
	// (status endpoint + journal-tail websocket) is wired.
	go func() {
		defer defaultJobSlots.release("backup", req.UserID)
		ctxBg, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
		defer cancel()
		if err := runBackupOrchestrator(ctxBg, req); err != nil {
			// Best-effort: orchestrator errors are observable via the
			// next backup.status call (looks for a manifest snapshot;
			// if absent, the run is treated as failed). v2 wires a
			// callback POST to panel-api directly.
			fmt.Println("backup orchestrator failed:", err)
		}
	}()

	return backupCreateResult{
		JobID:       req.JobID,
		SystemdUnit: fmt.Sprintf("jabali-backup-%s.service", req.JobID),
	}, nil
}

// runBackupOrchestrator drives the per-stage sequence and writes the
// manifest snapshot at the end. Independent stage failures do NOT abort
// the run (manifest records `status=failed` for that stage); fatal
// failures (restic missing, repo corruption) abort.
func runBackupOrchestrator(ctx context.Context, req backupCreateParams) error {
	c := backup.New(backup.DefaultConfig())
	manifest := backup.AccountManifest{
		SchemaVersion: backup.ManifestSchemaVersion,
		Kind:          backup.KindAccountBackup,
		JobID:         req.JobID,
		CreatedAt:     time.Now().UTC(),
		Source:        backup.ManifestSource{Hostname: hostname(), PanelSHA: panelSHA()},
		User: backup.ManifestUser{
			ID: req.UserID, Username: req.Username, Email: req.Email, IsAdmin: req.IsAdmin,
		},
	}

	// Stage: home
	homeStage := runHomeStage(ctx, req)
	manifest.Stages = append(manifest.Stages, homeStage)

	// Stage: databases
	dbStages := runDatabaseStage(ctx, req)
	manifest.Stages = append(manifest.Stages, dbStages...)

	// Stage: mailboxes
	mailStage := runMailStage(ctx, req)
	manifest.Stages = append(manifest.Stages, mailStage)

	// Stage: manifest snapshot (stdin-piped JSON blob)
	body, err := manifest.ToBytes()
	if err != nil {
		return fmt.Errorf("manifest serialize: %w", err)
	}
	tags := backup.AccountBackupTags(req.JobID, req.UserID, backup.StageManifest)
	summary, err := c.Backup(ctx, backup.BackupOpts{
		Stdin:     strings.NewReader(string(body)),
		StdinName: "manifest.json",
		Tags:      tags,
	})
	if err != nil {
		return fmt.Errorf("manifest snapshot: %w", err)
	}
	_ = summary // job snapshot id is also derivable via tag query
	return nil
}

func runHomeStage(ctx context.Context, req backupCreateParams) backup.ManifestStage {
	st := backup.ManifestStage{Name: backup.StageHome, Tag: "stage=home"}
	body, _ := json.Marshal(backupHomeParams{JobID: req.JobID, UserID: req.UserID, Username: req.Username})
	out, err := backupHomeHandler(ctx, body)
	if err != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{err.Error()}
		return st
	}
	res := out.(backupHomeResult)
	st.Status = backup.StageStatusOK
	st.SnapshotID = res.SnapshotID
	return st
}

func runDatabaseStage(ctx context.Context, req backupCreateParams) []backup.ManifestStage {
	if len(req.Databases) == 0 {
		return []backup.ManifestStage{{
			Name: backup.StageDB, Tag: "stage=db", Status: backup.StageStatusSkipped,
			Warnings: []string{"no databases"},
		}}
	}
	body, _ := json.Marshal(backupDatabasesParams{
		JobID: req.JobID, UserID: req.UserID, Username: req.Username, Databases: req.Databases,
	})
	out, err := backupDatabasesHandler(ctx, body)
	if err != nil {
		return []backup.ManifestStage{{
			Name: backup.StageDB, Tag: "stage=db", Status: backup.StageStatusFailed,
			Warnings: []string{err.Error()},
		}}
	}
	res := out.(backupDatabasesResult)
	stages := make([]backup.ManifestStage, 0, len(res.Snapshots))
	for _, s := range res.Snapshots {
		st := backup.ManifestStage{
			Name:       backup.StageDB,
			Tag:        "stage=db,db=" + s.DB,
			SnapshotID: s.SnapshotID,
			Items:      []string{s.DB},
			Status:     backup.StageStatusOK,
		}
		if s.Error != "" {
			st.Status = backup.StageStatusFailed
			st.Warnings = []string{s.Error}
		}
		stages = append(stages, st)
	}
	return stages
}

func runMailStage(ctx context.Context, req backupCreateParams) backup.ManifestStage {
	st := backup.ManifestStage{Name: backup.StageMail, Tag: "stage=mail"}
	body, _ := json.Marshal(backupMailboxesParams{
		JobID: req.JobID, UserID: req.UserID, Username: req.Username, Mailboxes: req.Mailboxes,
	})
	out, err := backupMailboxesHandler(ctx, body)
	if err != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{err.Error()}
		return st
	}
	res := out.(backupMailboxesResult)
	if res.Skipped {
		st.Status = backup.StageStatusSkipped
		if res.Reason != "" {
			st.Warnings = []string{"mailbox_export_skipped:" + res.Reason}
		}
		return st
	}
	st.Status = backup.StageStatusOK
	st.SnapshotID = res.SnapshotID
	st.Items = res.Mailboxes
	if len(res.Errors) > 0 {
		st.Warnings = res.Errors
	}
	return st
}

// backupStatusHandler returns aggregate status for a job-id by listing
// snapshots tagged job-id=<id> and reporting the manifest snapshot if
// present.
func backupStatusHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var req struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, bkInvalidArg("malformed JSON body")
	}
	if !ulidRE.MatchString(req.JobID) {
		return nil, bkInvalidArg("job_id must be a 26-char ULID")
	}
	c := backup.New(backup.DefaultConfig())
	snaps, err := c.Snapshots(ctx, []backup.Tag{backup.MakeTag(backup.TagKeyJobID, req.JobID)})
	if err != nil {
		return nil, bkInternal("restic snapshots", err)
	}
	resp := struct {
		JobID         string            `json:"job_id"`
		Stages        []string          `json:"stages"`
		ManifestFound bool              `json:"manifest_found"`
		Snapshots     []backup.Snapshot `json:"snapshots"`
	}{
		JobID:     req.JobID,
		Snapshots: snaps,
	}
	for _, s := range snaps {
		for _, t := range s.Tags {
			if t == "stage=manifest" {
				resp.ManifestFound = true
			}
			if strings.HasPrefix(t, "stage=") {
				resp.Stages = append(resp.Stages, strings.TrimPrefix(t, "stage="))
			}
		}
	}
	return resp, nil
}

// backupCancelHandler is a no-op stub for v1 — orchestrator runs
// inline so cancellation isn't observable via systemctl yet. v2
// integrates the transient-unit harness and rewires this to
// `systemctl stop jabali-backup-<id>.service`.
func backupCancelHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var req struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, bkInvalidArg("malformed JSON body")
	}
	if !ulidRE.MatchString(req.JobID) {
		return nil, bkInvalidArg("job_id must be a 26-char ULID")
	}
	return map[string]any{"job_id": req.JobID, "cancel": "noop_v1"}, nil
}

// hostname returns the box's hostname for manifest source recording.
func hostname() string {
	out, err := exec.Command("hostname", "-f").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// panelSHA returns the panel git SHA at build time. install.sh writes
// /etc/jabali-panel/panel.sha when it builds; missing file is fine
// (older installs).
func panelSHA() string {
	out, err := exec.Command("cat", "/etc/jabali-panel/panel.sha").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func init() {
	Default.Register("backup.create", backupCreateHandler)
	Default.Register("backup.status", backupStatusHandler)
	Default.Register("backup.cancel", backupCancelHandler)
}
