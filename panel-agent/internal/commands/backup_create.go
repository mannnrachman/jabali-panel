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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
)

// jobSlot tracks the single-active-job-per-(kind, user, destination)
// invariant. Per ADR-0080 each backup writes to one destination, and
// different destinations for the same user can run concurrently —
// they touch different repos so there's no restic lock contention.
type jobSlot struct {
	mu   sync.Mutex
	open map[string]string // key="kind:user_id:repo" → job_id
}

var defaultJobSlots = &jobSlot{open: map[string]string{}}

func (s *jobSlot) acquire(kind, userID, repoURL, jobID string) (existing string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := kind + ":" + userID + ":" + repoURL
	if cur, occupied := s.open[key]; occupied {
		return cur, false
	}
	s.open[key] = jobID
	return "", true
}

func (s *jobSlot) release(kind, userID, repoURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.open, kind+":"+userID+":"+repoURL)
}

type backupCreateParams struct {
	JobID      string   `json:"job_id"`
	UserID     string   `json:"user_id"`
	Username   string   `json:"username"`
	Email      string   `json:"email,omitempty"`
	IsAdmin    bool     `json:"is_admin"`
	Databases  []string `json:"databases,omitempty"`
	Mailboxes  []string `json:"mailboxes,omitempty"`
	// ScheduleID is the originating backup_schedules.id when the job
	// was enqueued by the in-process scheduler. Empty for manual
	// admin-create jobs. When set, snapshots receive a
	// `schedule-id=<id>` tag so per-schedule retention can target them.
	ScheduleID string `json:"schedule_id,omitempty"`
	// RepoURL is the restic repo URL the backup writes to (per
	// ADR-0080: each backup goes directly to one destination — local
	// path, sftp:..., s3:..., etc.). Empty falls back to the legacy
	// local repo at /var/lib/jabali-backups/repo for back-compat with
	// pre-M30.2 callers.
	RepoURL string `json:"repo_url,omitempty"`
	// CredentialsRef is the absolute path to the 0600 root:root env
	// file holding backend credentials (AWS_*, B2_*, SSHPASS, …).
	// Loaded by the agent and merged into the restic process env.
	CredentialsRef string `json:"credentials_ref,omitempty"`
	// ExtraOptions are restic `-o key=value` flag bodies — typically
	// `sftp.command="..."` for SFTP destinations with non-default
	// auth/port/key.
	ExtraOptions []string `json:"extra_options,omitempty"`
	// DestinationKind is the BackupDestination.Kind ("local","sftp",…)
	// — drives auto-mkdir for SFTP repos that don't yet exist on the
	// remote host.
	DestinationKind string `json:"destination_kind,omitempty"`
	// SFTP carries the SFTPInputs needed for `ssh ... mkdir -p` when
	// the target path is missing. Ignored for non-SFTP backends.
	SFTP *backupSFTPInputs `json:"sftp,omitempty"`
	// Metadata is the JSON-shaped panel-side state bundle produced
	// by panel-api (database users, app installs, ssh keys, …) and
	// persisted as a stage=metadata snapshot. Optional — empty bundle
	// is allowed and just produces an empty manifest of stage entries.
	Metadata *backup.AccountMetadata `json:"metadata,omitempty"`
}

// backupSFTPInputs mirrors panel-api models.SFTPOptions on the wire.
type backupSFTPInputs struct {
	Host    string `json:"host"`
	User    string `json:"user"`
	Port    int    `json:"port,omitempty"`
	Path    string `json:"path"`
	Auth    string `json:"auth"`
	KeyPath string `json:"key_path,omitempty"`
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

	if existing, ok := defaultJobSlots.acquire("backup", req.UserID, req.RepoURL, req.JobID); !ok {
		return nil, fmt.Errorf("backup already running for user_id=%s repo=%s job=%s", req.UserID, req.RepoURL, existing)
	}

	// Inline orchestration for v1. Future split into a transient
	// systemd unit happens once the panel-side observation surface
	// (status endpoint + journal-tail websocket) is wired.
	go func() {
		defer defaultJobSlots.release("backup", req.UserID, req.RepoURL)
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
	jl := backup.NewJobLogger(req.JobID)
	defer jl.Close()
	jl.Printf("account_backup start user_id=%s username=%s databases=%d mailboxes=%d destination=%s",
		req.UserID, req.Username, len(req.Databases), len(req.Mailboxes), req.RepoURL)
	if err := bkEnsureRepoReady(ctx, req.RepoURL, req.CredentialsRef, req.DestinationKind, req.SFTP, req.ExtraOptions); err != nil {
		jl.Printf("ensure_repo_failed=%v", err)
		return fmt.Errorf("ensure repo: %w", err)
	}
	cfg, cerr := bkResticConfig(req.RepoURL, req.CredentialsRef, req.ExtraOptions)
	if cerr != nil {
		return fmt.Errorf("restic config: %w", cerr)
	}
	c := backup.New(cfg)
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
	jl.Printf("stage=home start")
	homeStage := runHomeStage(ctx, req)
	jl.Printf("stage=home done status=%s bytes_added=%d bytes_total=%d warnings=%v",
		homeStage.Status, homeStage.BytesAdded, homeStage.BytesTotal, homeStage.Warnings)
	manifest.Stages = append(manifest.Stages, homeStage)

	// Stage: databases
	jl.Printf("stage=db start dbs=%v", req.Databases)
	dbStages := runDatabaseStage(ctx, req)
	for _, s := range dbStages {
		jl.Printf("stage=db done item=%v status=%s bytes_added=%d bytes_total=%d warnings=%v",
			s.Items, s.Status, s.BytesAdded, s.BytesTotal, s.Warnings)
	}
	manifest.Stages = append(manifest.Stages, dbStages...)

	// Stage: mailboxes
	jl.Printf("stage=mail start mailboxes=%v", req.Mailboxes)
	mailStage := runMailStage(ctx, req)
	jl.Printf("stage=mail done status=%s bytes_added=%d bytes_total=%d warnings=%v",
		mailStage.Status, mailStage.BytesAdded, mailStage.BytesTotal, mailStage.Warnings)
	manifest.Stages = append(manifest.Stages, mailStage)

	// Stage: metadata (DB users, app installs, …) — sidecar JSON
	// produced by panel-api and shipped via the call params.
	jl.Printf("stage=meta start")
	metaStage := runMetadataStage(ctx, req)
	jl.Printf("stage=meta done status=%s bytes_added=%d bytes_total=%d warnings=%v",
		metaStage.Status, metaStage.BytesAdded, metaStage.BytesTotal, metaStage.Warnings)
	manifest.Stages = append(manifest.Stages, metaStage)

	// Sum stage byte counts into the top-level ManifestRestic block so
	// finalizer + DTO consumers don't have to re-walk stages[]. SnapshotID
	// at top-level is set after the manifest snapshot itself completes —
	// here we only fill totals.
	for _, s := range manifest.Stages {
		manifest.Restic.BytesAdded += s.BytesAdded
		manifest.Restic.BytesTotal += s.BytesTotal
	}

	// Stage: manifest snapshot (stdin-piped JSON blob)
	jl.Printf("stage=manifest start bytes_added_total=%d bytes_total_total=%d",
		manifest.Restic.BytesAdded, manifest.Restic.BytesTotal)
	body, err := manifest.ToBytes()
	if err != nil {
		jl.Printf("stage=manifest serialize_err=%v", err)
		return fmt.Errorf("manifest serialize: %w", err)
	}
	tags := backup.AccountBackupTags(req.JobID, req.UserID, req.ScheduleID, backup.StageManifest)
	summary, err := c.Backup(ctx, backup.BackupOpts{
		Stdin:     strings.NewReader(string(body)),
		StdinName: "manifest.json",
		Tags:      tags,
	})
	if err != nil {
		jl.Printf("stage=manifest restic_err=%v", err)
		return fmt.Errorf("manifest snapshot: %w", err)
	}
	_ = summary // job snapshot id is also derivable via tag query
	jl.Printf("account_backup done")
	return nil
}

func runHomeStage(ctx context.Context, req backupCreateParams) backup.ManifestStage {
	st := backup.ManifestStage{Name: backup.StageHome, Tag: "stage=home"}
	body, _ := json.Marshal(backupHomeParams{
		JobID: req.JobID, UserID: req.UserID, Username: req.Username,
		ScheduleID: req.ScheduleID, RepoURL: req.RepoURL,
		CredentialsRef: req.CredentialsRef, ExtraOptions: req.ExtraOptions,
	})
	out, err := backupHomeHandler(ctx, body)
	if err != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{err.Error()}
		return st
	}
	res := out.(backupHomeResult)
	st.Status = backup.StageStatusOK
	st.SnapshotID = res.SnapshotID
	st.BytesAdded = res.BytesAdded
	st.BytesTotal = res.BytesTotal
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
		JobID: req.JobID, UserID: req.UserID, Username: req.Username,
		Databases: req.Databases, ScheduleID: req.ScheduleID,
		RepoURL: req.RepoURL, CredentialsRef: req.CredentialsRef,
		ExtraOptions: req.ExtraOptions,
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
			BytesAdded: s.BytesAdded,
			BytesTotal: s.BytesTotal,
		}
		if s.Error != "" {
			st.Status = backup.StageStatusFailed
			st.Warnings = []string{s.Error}
		}
		stages = append(stages, st)
	}
	return stages
}

// runMetadataStage writes the panel-side bundle (DB users, app
// installs, …) as one stdin-piped restic snapshot tagged stage=meta.
// Empty/nil Metadata produces a "skipped: no metadata" stage so
// restore-side knows to fall back to manual recreation.
func runMetadataStage(ctx context.Context, req backupCreateParams) backup.ManifestStage {
	st := backup.ManifestStage{Name: backup.StageMeta, Tag: "stage=" + backup.StageMeta}
	if req.Metadata == nil {
		st.Status = backup.StageStatusSkipped
		st.Warnings = []string{"no metadata bundle provided by panel-api"}
		return st
	}
	body, err := json.Marshal(req.Metadata)
	if err != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{"metadata marshal: " + err.Error()}
		return st
	}
	cfg, cerr := bkResticConfig(req.RepoURL, req.CredentialsRef, req.ExtraOptions)
	if cerr != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{"restic config: " + cerr.Error()}
		return st
	}
	c := backup.New(cfg)
	tags := backup.AccountBackupTags(req.JobID, req.UserID, req.ScheduleID, backup.StageMeta)
	summary, err := c.Backup(ctx, backup.BackupOpts{
		Stdin:     strings.NewReader(string(body)),
		StdinName: "metadata.json",
		Tags:      tags,
	})
	if err != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{"restic backup metadata: " + err.Error()}
		return st
	}
	st.Status = backup.StageStatusOK
	st.SnapshotID = summary.SnapshotID
	st.BytesAdded = summary.DataAdded
	st.BytesTotal = summary.TotalBytesProcessed
	return st
}

func runMailStage(ctx context.Context, req backupCreateParams) backup.ManifestStage {
	st := backup.ManifestStage{Name: backup.StageMail, Tag: "stage=mail"}
	body, _ := json.Marshal(backupMailboxesParams{
		JobID: req.JobID, UserID: req.UserID, Username: req.Username,
		Mailboxes: req.Mailboxes, ScheduleID: req.ScheduleID,
		RepoURL: req.RepoURL, CredentialsRef: req.CredentialsRef,
		ExtraOptions: req.ExtraOptions,
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
	st.BytesAdded = res.BytesAdded
	st.BytesTotal = res.BytesTotal
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
		JobID          string   `json:"job_id"`
		RepoURL        string   `json:"repo_url,omitempty"`
		CredentialsRef string   `json:"credentials_ref,omitempty"`
		ExtraOptions   []string `json:"extra_options,omitempty"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, bkInvalidArg("malformed JSON body")
	}
	if !ulidRE.MatchString(req.JobID) {
		return nil, bkInvalidArg("job_id must be a 26-char ULID")
	}
	cfg, cerr := bkResticConfig(req.RepoURL, req.CredentialsRef, req.ExtraOptions)
	if cerr != nil {
		return nil, bkInternal("restic config", cerr)
	}
	c := backup.New(cfg)
	snaps, err := c.Snapshots(ctx, []backup.Tag{backup.MakeTag(backup.TagKeyJobID, req.JobID)})
	if err != nil {
		return nil, bkInternal("restic snapshots", err)
	}
	resp := struct {
		JobID         string            `json:"job_id"`
		Stages        []string          `json:"stages"`
		ManifestFound bool              `json:"manifest_found"`
		Snapshots     []backup.Snapshot `json:"snapshots"`
		// Sums from the manifest's Restic block when manifest_found.
		// Zero on system_backup (manifest schema doesn't carry totals
		// at top level) or when the manifest can't be read.
		BytesAdded uint64 `json:"bytes_added,omitempty"`
		BytesTotal uint64 `json:"bytes_total,omitempty"`
	}{
		JobID:     req.JobID,
		Snapshots: snaps,
	}
	manifestSnapID := ""
	for _, s := range snaps {
		for _, t := range s.Tags {
			if t == "stage=manifest" {
				resp.ManifestFound = true
				manifestSnapID = s.ID
			}
			if strings.HasPrefix(t, "stage=") {
				resp.Stages = append(resp.Stages, strings.TrimPrefix(t, "stage="))
			}
		}
	}
	if manifestSnapID != "" {
		// `restic dump` prints the file content as a tar stream when the
		// path is a directory but for a single file it streams the raw
		// bytes. account_backup writes /manifest.json; system_backup
		// writes /system_manifest.json — try the account name first and
		// fall through if absent so one path covers both kinds.
		raw, err := c.Dump(ctx, manifestSnapID, "/manifest.json")
		if err != nil || len(raw) == 0 {
			raw, err = c.Dump(ctx, manifestSnapID, "/system_manifest.json")
		}
		if err == nil && len(raw) > 0 {
			// account_backup manifest carries Restic.BytesAdded/Total.
			// system_backup manifest has the same shape under
			// Restic.{BytesAdded,BytesTotal} so a single parse works for
			// both via a small lenient struct.
			var m struct {
				Restic struct {
					BytesAdded uint64 `json:"bytes_added"`
					BytesTotal uint64 `json:"bytes_total"`
				} `json:"restic"`
				// stages[] also carries per-stage byte counts; sum them as
				// a fallback when Restic.BytesAdded is zero (some older
				// backup paths leave the top-level zero).
				Stages []struct {
					BytesAdded uint64 `json:"bytes_added"`
					BytesTotal uint64 `json:"bytes_total"`
				} `json:"stages"`
			}
			if err := json.Unmarshal(stripTar(raw), &m); err == nil {
				resp.BytesAdded = m.Restic.BytesAdded
				resp.BytesTotal = m.Restic.BytesTotal
				if resp.BytesAdded == 0 && resp.BytesTotal == 0 && len(m.Stages) > 0 {
					for _, st := range m.Stages {
						resp.BytesAdded += st.BytesAdded
						resp.BytesTotal += st.BytesTotal
					}
				}
			}
		}
	}
	return resp, nil
}

// stripTar pops a leading 512-byte tar header off the dump output if
// present. `restic dump <id> /file` returns raw bytes for a single file
// in modern restic; some versions wrap it in a tar archive. Header is
// detectable by the "ustar" magic at offset 257; file size is the octal
// number at offset 124 (12 bytes).
func stripTar(b []byte) []byte {
	if len(b) <= 512 || !bytes.HasPrefix(b[257:], []byte("ustar")) {
		return b
	}
	sizeField := bytes.TrimSpace(bytes.Trim(b[124:124+12], "\x00"))
	var size int64
	for _, c := range sizeField {
		if c < '0' || c > '7' {
			break
		}
		size = size*8 + int64(c-'0')
	}
	if size <= 0 || 512+size > int64(len(b)) {
		return b
	}
	return b[512 : 512+size]
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
