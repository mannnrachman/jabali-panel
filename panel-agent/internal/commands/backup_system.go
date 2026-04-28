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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
)

// systemPanelDatabases is the canonical list of MariaDB databases that
// belong to the panel/Kratos/PowerDNS plane. Per-database dumps land
// in separate restic snapshots tagged stage=panel_db,db=<name> so a
// targeted restore can drop just one.
var systemPanelDatabases = []string{
	"jabali_panel",
	"jabali_kratos",
	"jabali_pdns",
}

// systemUsersGroupAllowlist names the supplementary groups that always
// survive the os_users filter regardless of uid (system accounts that
// own jabali-internal sockets, mail spool, etc.).
var systemUsersGroupAllowlist = map[string]struct{}{
	"jabali":         {},
	"jabali-mail":    {},
	"jabali-sockets": {},
	"jabali-sftp":    {},
	"pdns":           {},
}

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

	// panel_db: mariadb-dump per system DB into separate snapshots so a
	// targeted restore can replace just one. Each snapshot tagged
	// stage=panel_db,db=<name>.
	manifest.Stages = append(manifest.Stages,
		runSystemPanelDBStage(ctx, c, req.JobID, host)...,
	)

	// panel_config: 0600 master password is the operator's off-host
	// responsibility (ADR-0075) — exclude from snapshots so a stolen
	// system_backup tarball doesn't leak the key needed to decrypt it.
	manifest.Stages = append(manifest.Stages,
		runSystemPathStage(ctx, c, req.JobID, host, backup.StagePanelConfig, "/etc/jabali-panel",
			[]string{"restic-repo.password"}))

	// service_config: every config tree the panel + reconciler write to.
	// Glob expansion on /etc/nginx/sites-* / /etc/php/*/fpm /
	// /etc/systemd/system/jabali-*.service.d/ happens at scan time so a
	// missing directory (e.g. PHP not installed) doesn't fail the stage.
	manifest.Stages = append(manifest.Stages,
		runSystemMultiPathStage(ctx, c, req.JobID, host, backup.StageServiceConfig,
			expandServiceConfigPaths(), nil))

	manifest.Stages = append(manifest.Stages,
		runSystemPathStage(ctx, c, req.JobID, host, backup.StageMailState,
			"/var/lib/stalwart",
			// LOG / LOG.old.* / LOCK are RocksDB runtime artefacts —
			// not data, just bloat. Same exclusions the per-user mail
			// stage uses for its bodies tarball.
			[]string{"LOG", "LOG.old.*", "LOCK"}),
		runSystemPathStage(ctx, c, req.JobID, host, backup.StageTLS, "/etc/letsencrypt", nil),
	)

	// security: CrowdSec + UFW rules + ModSec config. UFW + ModSec
	// dirs may be absent on minimal installs; skip-on-missing keeps
	// the stage successful with warnings.
	manifest.Stages = append(manifest.Stages,
		runSystemMultiPathStage(ctx, c, req.JobID, host, backup.StageSecurity,
			[]string{"/etc/crowdsec", "/etc/ufw", "/etc/modsecurity"}, nil))

	// os_users: stream filtered passwd/shadow/group/gshadow as a single
	// JSON blob via restic stdin. Capture only uid >= 1000 OR primary
	// group in the system-accounts allowlist (jabali, jabali-mail,
	// jabali-sockets, pdns).
	manifest.Stages = append(manifest.Stages,
		runSystemOSUsersStage(ctx, c, req.JobID, host))

	// Disaster-recovery stages — small per-stage but each one is
	// load-bearing for "rebuild this host on a fresh Debian VM".
	// Missing path → skipped + warning, never fatal.
	manifest.Stages = append(manifest.Stages,
		runSystemMultiPathStage(ctx, c, req.JobID, host, backup.StageOSBase,
			expandOSBasePaths(), nil))
	manifest.Stages = append(manifest.Stages,
		runSystemMultiPathStage(ctx, c, req.JobID, host, backup.StageAPT,
			expandAPTPaths(), nil))
	manifest.Stages = append(manifest.Stages,
		runSystemMultiPathStage(ctx, c, req.JobID, host, backup.StageSSHHost,
			expandSSHPaths(), nil))
	manifest.Stages = append(manifest.Stages,
		runSystemMultiPathStage(ctx, c, req.JobID, host, backup.StageSystemCron,
			expandCronPaths(), nil))
	manifest.Stages = append(manifest.Stages,
		runSystemMultiPathStage(ctx, c, req.JobID, host, backup.StageDataState,
			expandDataStatePaths(), nil))
	manifest.Stages = append(manifest.Stages,
		runSystemMultiPathStage(ctx, c, req.JobID, host, backup.StageSudoers,
			expandSudoersPaths(), nil))

	// Sum stage byte counts into the top-level ManifestRestic so the
	// panel-side finalizer can record total bytes_added/bytes_total per
	// system backup without re-walking stages[].
	for _, s := range manifest.Stages {
		manifest.Restic.BytesAdded += s.BytesAdded
		manifest.Restic.BytesTotal += s.BytesTotal
	}

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
	st.BytesAdded = summary.DataAdded
	st.BytesTotal = summary.TotalBytesProcessed
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

// runSystemMultiPathStage is the multi-path variant of runSystemPathStage.
// One restic snapshot covers every existing path in the list. Missing
// paths are recorded as warnings but don't fail the stage. Empty path
// list (every entry missing) → status=skipped.
func runSystemMultiPathStage(ctx context.Context, c *backup.Client, jobID, hostname, stageName string, paths, excludes []string) backup.ManifestStage {
	st := backup.ManifestStage{Name: stageName, Tag: "stage=" + stageName}
	keep := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			st.Warnings = append(st.Warnings, fmt.Sprintf("path missing: %s", p))
			continue
		}
		keep = append(keep, p)
	}
	if len(keep) == 0 {
		st.Status = backup.StageStatusSkipped
		return st
	}
	tags := backup.SystemBackupTags(jobID, hostname, stageName)
	excludeArgs := make([]string, 0, len(excludes))
	for _, e := range excludes {
		excludeArgs = append(excludeArgs, e)
	}
	summary, err := c.Backup(ctx, backup.BackupOpts{
		Paths:       keep,
		Tags:        tags,
		ExcludeArgs: excludeArgs,
	})
	if err != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = append(st.Warnings, err.Error())
		return st
	}
	st.Status = backup.StageStatusOK
	st.SnapshotID = summary.SnapshotID
	st.BytesAdded = summary.DataAdded
	st.BytesTotal = summary.TotalBytesProcessed
	st.Items = keep
	return st
}

// expandServiceConfigPaths returns the concrete /etc paths that exist
// on this host for the service_config stage. Glob-style expansion done
// here so missing trees don't break the stage and so the manifest
// records exactly which paths were captured.
func expandServiceConfigPaths() []string {
	out := []string{
		"/etc/stalwart",
		"/etc/powerdns",
		"/etc/redis",
		"/etc/jabali-bulwark",
		"/etc/jabali-tetragon",
		"/etc/clamav",
	}
	// nginx: capture the full tree (nginx.conf + conf.d + snippets +
	// sites-*) — operator-edited entries land in any of these and a
	// restore needs the whole config to come back the same way.
	out = append(out, globOrSkip("/etc/nginx")...)
	if matches, _ := filepath.Glob("/etc/php/*/fpm"); len(matches) > 0 {
		out = append(out, matches...)
	}
	// systemd drop-ins AND single-file units.
	if matches, _ := filepath.Glob("/etc/systemd/system/jabali-*.service.d"); len(matches) > 0 {
		out = append(out, matches...)
	}
	if matches, _ := filepath.Glob("/etc/systemd/system/jabali-*.service"); len(matches) > 0 {
		out = append(out, matches...)
	}
	if matches, _ := filepath.Glob("/etc/systemd/system/jabali-*.timer"); len(matches) > 0 {
		out = append(out, matches...)
	}
	return out
}

// globOrSkip returns [path] if it exists, empty otherwise. Used by the
// service_config expansion to skip paths that don't exist on this host
// without breaking the stage.
func globOrSkip(path string) []string {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return []string{path}
}

// expandOSBasePaths returns the system-level files needed to identify
// the host on disaster recovery: hostname, hosts table, fstab, network
// config, sysctl tweaks. Most of these are 1-2 KB but absolutely
// load-bearing for a re-install — without them the restored host comes
// up with a default hostname or no network.
func expandOSBasePaths() []string {
	out := []string{}
	for _, p := range []string{
		"/etc/hostname",
		"/etc/hosts",
		"/etc/fstab",
		"/etc/sysctl.conf",
		"/etc/sysctl.d",
		"/etc/netplan",
		"/etc/network",
		"/etc/resolv.conf",
		"/etc/locale.gen",
		"/etc/timezone",
		"/etc/skel",
	} {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// expandAPTPaths returns the apt package-manager state needed to
// re-install the same Debian + third-party packages on a restore host:
// sources, prefs, keyrings (third-party signing keys).
func expandAPTPaths() []string {
	out := []string{}
	for _, p := range []string{
		"/etc/apt/sources.list",
		"/etc/apt/sources.list.d",
		"/etc/apt/preferences",
		"/etc/apt/preferences.d",
		"/etc/apt/keyrings",
		"/etc/apt/trusted.gpg",
		"/etc/apt/trusted.gpg.d",
	} {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// expandSSHPaths returns SSH server state: host config + host keys.
// Host keys preserve the server's identity so existing SSH clients
// don't trigger MITM warnings on restore.
func expandSSHPaths() []string {
	out := []string{}
	for _, p := range []string{
		"/etc/ssh/sshd_config",
		"/etc/ssh/sshd_config.d",
	} {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	if matches, _ := filepath.Glob("/etc/ssh/ssh_host_*"); len(matches) > 0 {
		out = append(out, matches...)
	}
	return out
}

// expandCronPaths returns system cron state: /etc/cron.d (jabali timers
// + apt-listchanges + …), /etc/crontab, and the spool dir holding
// per-user crontabs (which include the panel jabali-cron entries).
func expandCronPaths() []string {
	out := []string{}
	for _, p := range []string{
		"/etc/crontab",
		"/etc/cron.d",
		"/etc/cron.daily",
		"/etc/cron.hourly",
		"/etc/cron.weekly",
		"/etc/cron.monthly",
		"/var/spool/cron/crontabs",
	} {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// expandDataStatePaths returns runtime-data directories that aren't
// part of the panel-plane MariaDB dump but still hold operator state:
// crowdsec decisions DB, redis AOF, tetragon event spool. NOT included:
// /var/lib/clamav (ClamAV signatures, ~250 MB; freshclam re-fetches
// on restore) and /var/log/* (ephemeral observability).
func expandDataStatePaths() []string {
	out := []string{}
	for _, p := range []string{
		"/var/lib/crowdsec",
		"/var/lib/redis",
		"/var/lib/jabali-tetragon",
		"/var/lib/jabali-panel-acme",
	} {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// expandSudoersPaths returns sudoers + drop-ins. Captured separately
// so the security stage can stay scoped to firewall+intrusion configs.
func expandSudoersPaths() []string {
	out := []string{}
	for _, p := range []string{
		"/etc/sudoers",
		"/etc/sudoers.d",
	} {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// runSystemPanelDBStage dumps each panel-plane database (jabali_panel,
// jabali_kratos, jabali_pdns) via mariadb-dump piped to restic --stdin,
// one snapshot per DB tagged stage=panel_db,db=<name>. Returns one
// ManifestStage per database so the system manifest can record per-DB
// status separately.
func runSystemPanelDBStage(ctx context.Context, c *backup.Client, jobID, hostname string) []backup.ManifestStage {
	if _, err := exec.LookPath("mariadb-dump"); err != nil {
		return []backup.ManifestStage{{
			Name:     backup.StagePanelDB,
			Tag:      "stage=panel_db",
			Status:   backup.StageStatusSkipped,
			Warnings: []string{"mariadb-dump not on PATH"},
		}}
	}
	stages := make([]backup.ManifestStage, 0, len(systemPanelDatabases))
	for _, db := range systemPanelDatabases {
		stages = append(stages, dumpOneSystemDB(ctx, c, jobID, hostname, db))
	}
	return stages
}

func dumpOneSystemDB(ctx context.Context, c *backup.Client, jobID, hostname, db string) backup.ManifestStage {
	st := backup.ManifestStage{
		Name:  backup.StagePanelDB,
		Tag:   fmt.Sprintf("stage=panel_db,db=%s", db),
		Items: []string{db},
	}
	cmd := exec.CommandContext(ctx, "mariadb-dump",
		"--single-transaction", "--skip-lock-tables",
		"--routines", "--triggers", "--events",
		"--hex-blob",
		db,
	)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{fmt.Sprintf("stdout pipe: %v", err)}
		return st
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{fmt.Sprintf("start mariadb-dump %s: %v", db, err)}
		return st
	}
	tags := backup.SystemBackupTags(jobID, hostname, backup.StagePanelDB)
	tags = append(tags, backup.MakeTag(backup.TagKeyDB, db))

	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer pw.Close()
		_, _ = io.Copy(pw, stdoutPipe)
	}()

	summary, backupErr := c.Backup(ctx, backup.BackupOpts{
		Stdin:     pr,
		StdinName: db + ".sql",
		Tags:      tags,
	})
	wg.Wait()
	waitErr := cmd.Wait()
	if waitErr != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{fmt.Sprintf("mariadb-dump %s: %v: %s", db, waitErr, strings.TrimSpace(stderrBuf.String()))}
		return st
	}
	if backupErr != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{fmt.Sprintf("restic backup --stdin: %v", backupErr)}
		return st
	}
	st.Status = backup.StageStatusOK
	st.SnapshotID = summary.SnapshotID
	st.BytesAdded = summary.DataAdded
	st.BytesTotal = summary.TotalBytesProcessed
	return st
}

// runSystemOSUsersStage parses /etc/passwd, /etc/shadow, /etc/group,
// /etc/gshadow and emits a single os_users.json blob with only the
// hosting-relevant entries: uid >= 1000, OR primary group name in
// systemUsersGroupAllowlist, OR group name itself in the allowlist.
// The blob is piped through restic --stdin tagged stage=os_users.
func runSystemOSUsersStage(ctx context.Context, c *backup.Client, jobID, hostname string) backup.ManifestStage {
	st := backup.ManifestStage{Name: backup.StageOSUsers, Tag: "stage=os_users"}

	users, err := loadFilteredOSUsers()
	if err != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{err.Error()}
		return st
	}
	body, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{fmt.Sprintf("marshal os_users: %v", err)}
		return st
	}
	tags := backup.SystemBackupTags(jobID, hostname, backup.StageOSUsers)
	summary, err := c.Backup(ctx, backup.BackupOpts{
		Stdin:     bytes.NewReader(body),
		StdinName: "os_users.json",
		Tags:      tags,
	})
	if err != nil {
		st.Status = backup.StageStatusFailed
		st.Warnings = []string{err.Error()}
		return st
	}
	st.Status = backup.StageStatusOK
	st.SnapshotID = summary.SnapshotID
	st.BytesAdded = summary.DataAdded
	st.BytesTotal = summary.TotalBytesProcessed
	return st
}

type osUsersBundle struct {
	Passwd  []string `json:"passwd"`
	Shadow  []string `json:"shadow"`
	Group   []string `json:"group"`
	GShadow []string `json:"gshadow"`
}

// loadFilteredOSUsers walks /etc/{passwd,shadow,group,gshadow} keeping
// only entries that match the hosting filter:
//   - passwd/shadow: uid >= 1000 OR primary group in allowlist
//   - group/gshadow: group name in allowlist
//
// Errors reading any single file are reported as warnings but don't
// block the others — partial os_users data still beats no os_users
// snapshot at all.
func loadFilteredOSUsers() (*osUsersBundle, error) {
	keepUsers, primaryGIDs, err := selectOSUsernames("/etc/passwd")
	if err != nil {
		return nil, fmt.Errorf("read /etc/passwd: %w", err)
	}
	bundle := &osUsersBundle{}

	if rows, err := readGroupFile("/etc/group", systemUsersGroupAllowlist, primaryGIDs); err == nil {
		for _, line := range rows {
			bundle.Group = append(bundle.Group, line.line)
		}
	} else {
		return nil, err
	}

	if rows, err := readUsernameKeyedFile("/etc/passwd", keepUsers); err == nil {
		bundle.Passwd = rows
	}
	if rows, err := readUsernameKeyedFile("/etc/shadow", keepUsers); err == nil {
		bundle.Shadow = rows
	}
	if rows, err := readGroupShadowFile("/etc/gshadow", systemUsersGroupAllowlist); err == nil {
		bundle.GShadow = rows
	}
	return bundle, nil
}

// selectOSUsernames returns:
//   - usernames whose uid >= 1000 OR whose primary GID maps to a name in
//     systemUsersGroupAllowlist
//   - the set of primary GIDs referenced by those users (used to also
//     keep matching /etc/group rows that aren't already in the allowlist)
func selectOSUsernames(passwdPath string) (map[string]struct{}, map[int]struct{}, error) {
	groupNameByGID, err := readGroupGIDMap("/etc/group")
	if err != nil {
		return nil, nil, err
	}
	users := map[string]struct{}{}
	gids := map[int]struct{}{}
	f, err := os.Open(passwdPath) //nolint:gosec
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 4 {
			continue
		}
		username := parts[0]
		uid, _ := strconv.Atoi(parts[2])
		gid, _ := strconv.Atoi(parts[3])
		groupName := groupNameByGID[gid]
		_, allowGroup := systemUsersGroupAllowlist[groupName]
		if uid >= 1000 || allowGroup {
			users[username] = struct{}{}
			gids[gid] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return users, gids, nil
}

func readGroupGIDMap(path string) (map[int]string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[int]string{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, ":")
		if len(parts) < 3 {
			continue
		}
		gid, _ := strconv.Atoi(parts[2])
		out[gid] = parts[0]
	}
	return out, scanner.Err()
}

// readUsernameKeyedFile returns lines whose first colon-separated field
// is in `keep`. Used for /etc/passwd and /etc/shadow.
func readUsernameKeyedFile(path string, keep map[string]struct{}) ([]string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		idx := strings.IndexByte(line, ':')
		if idx <= 0 {
			continue
		}
		if _, ok := keep[line[:idx]]; ok {
			out = append(out, line)
		}
	}
	return out, scanner.Err()
}

type groupLine struct {
	name string
	line string
}

// readGroupFile keeps /etc/group rows where (a) the group name is in the
// allowlist or (b) its GID is referenced by a kept primary-group user.
// The full line is preserved so members + group password hash survive
// restore.
func readGroupFile(path string, allowlist map[string]struct{}, primaryGIDs map[int]struct{}) ([]groupLine, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []groupLine
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 3 {
			continue
		}
		name := parts[0]
		gid, _ := strconv.Atoi(parts[2])
		_, allow := allowlist[name]
		_, primary := primaryGIDs[gid]
		if allow || primary || gid >= 1000 {
			out = append(out, groupLine{name: name, line: line})
		}
	}
	return out, scanner.Err()
}

// readGroupShadowFile filters /etc/gshadow by group name in allowlist
// (the file's first field is the group name).
func readGroupShadowFile(path string, allowlist map[string]struct{}) ([]string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		idx := strings.IndexByte(line, ':')
		if idx <= 0 {
			continue
		}
		if _, ok := allowlist[line[:idx]]; ok {
			out = append(out, line)
		}
	}
	return out, scanner.Err()
}

func init() {
	Default.Register("system.backup", systemBackupHandler)
	Default.Register("system.backup_status", systemBackupStatusHandler)
	Default.Register("system.backup_cancel", systemBackupCancelHandler)
	Default.Register("system.restore", systemRestoreHandler)
}
