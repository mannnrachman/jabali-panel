// Step 5 of M30: backup.mailboxes agent command. Stalwart-cli exports
// each mailbox to a per-job staging dir under /run/jabali-backup/<job-id>/
// (tmpfs). One restic snapshot bundles all mailboxes for the job.
//
// Stalwart down → return success with `mailbox_export_skipped:stalwart_down`
// warning, no snapshot created. Backup orchestrator records the warning
// and continues with other stages.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
)

type backupMailboxesParams struct {
	JobID          string   `json:"job_id"`
	UserID         string   `json:"user_id"`
	Username       string   `json:"username"`
	Mailboxes      []string `json:"mailboxes"`
	ScheduleID     string   `json:"schedule_id,omitempty"`
	RepoURL        string   `json:"repo_url,omitempty"`
	PasswordFile   string   `json:"password_file,omitempty"`
	CredentialsRef string   `json:"credentials_ref,omitempty"`
	ExtraOptions   []string `json:"extra_options,omitempty"`
}

type backupMailboxesResult struct {
	SnapshotID string   `json:"snapshot_id,omitempty"`
	BytesAdded uint64   `json:"bytes_added"`
	BytesTotal uint64   `json:"bytes_total"`
	Skipped    bool     `json:"skipped"`
	Reason     string   `json:"reason,omitempty"`
	Mailboxes  []string `json:"mailboxes"`
	Errors     []string `json:"errors,omitempty"`
}

const backupStagingRoot = "/run/jabali-backup"

// ErrStalwartDown is returned when the Stalwart service is inactive at
// backup time. Caller surfaces a `mailbox_export_skipped:stalwart_down`
// warning rather than failing the whole backup.
var ErrStalwartDown = errors.New("stalwart service inactive — mailbox stage skipped")

func backupMailboxesHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var req backupMailboxesParams
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
	for _, mb := range req.Mailboxes {
		local, domain, ok := splitMailbox(mb)
		if !ok {
			return nil, bkInvalidArg(fmt.Sprintf("mailbox not user@domain: %s", mb))
		}
		if !emailLocalRE.MatchString(local) || !emailDomainRE.MatchString(domain) {
			return nil, bkInvalidArg(fmt.Sprintf("mailbox contains forbidden characters: %s", mb))
		}
	}
	if len(req.Mailboxes) == 0 {
		return backupMailboxesResult{Skipped: true, Reason: "no mailboxes"}, nil
	}

	// Stalwart down? Skip with warning.
	if !stalwartActive(ctx) {
		return backupMailboxesResult{
			Skipped:   true,
			Reason:    "stalwart_down",
			Mailboxes: req.Mailboxes,
		}, nil
	}

	if _, err := bkResticBin(); err != nil {
		return nil, bkInternal("restic missing", err)
	}
	if _, err := exec.LookPath("stalwart-cli"); err != nil {
		return backupMailboxesResult{
			Skipped:   true,
			Reason:    "stalwart_cli_missing",
			Mailboxes: req.Mailboxes,
		}, nil
	}

	// v1 mail stage uses the Stalwart admin CLI to snapshot the per-
	// user account configuration (Account/AccountPassword/Identity/
	// MailboxConfig/SieveScript/Vacation… as supported by Stalwart's
	// `snapshot` verb) into a JSON plan file, then bundles that plan
	// PLUS the maildir-equivalent message bodies into one restic
	// snapshot tagged stage=mail per job.
	//
	// stalwart-cli snapshot writes a plan consumable by `apply`, which
	// is exactly what restore-side needs: one CLI call recreates the
	// principal + every per-account setting. Message bodies still
	// require pulling from Stalwart's RocksDB; for v1 we include the
	// admin-side dump alongside the plan as a tarball mounted from
	// /var/lib/stalwart so a per-user restore stays self-contained.
	adminURL, adminUser, adminPass := stalwartAdminCreds()
	if adminURL == "" || adminUser == "" || adminPass == "" {
		return backupMailboxesResult{
			Skipped:   true,
			Reason:    "stalwart_admin_creds_missing",
			Mailboxes: req.Mailboxes,
		}, nil
	}

	stagingDir := filepath.Join(backupStagingRoot, req.JobID, "mail")
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return nil, bkInternal("mkdir staging", err)
	}
	defer os.RemoveAll(stagingDir)

	planPath := filepath.Join(stagingDir, "plan.json")
	// Snapshot the per-user account objects. --allow-unresolved keeps
	// references to system-wide types (Domain, Tenant, Role) that
	// already exist on the destination from forming hard refs in the
	// plan. Without it Stalwart refuses to emit Account because Domain
	// etc. aren't in the snapshot scope.
	args := []string{
		"snapshot", "Account",
		"--allow-unresolved", "Domain,Tenant,Role,DkimSignature,PublicKey",
		"--output", planPath,
		"--quiet", "--no-color",
	}
	cmd := exec.CommandContext(ctx, "stalwart-cli", args...)
	cmd.Env = append(os.Environ(),
		"STALWART_URL="+adminURL,
		"STALWART_USER="+adminUser,
		"STALWART_PASSWORD="+adminPass,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return backupMailboxesResult{
			Skipped:   true,
			Reason:    fmt.Sprintf("stalwart_snapshot_failed: %v: %s", err, strings.TrimSpace(string(out))),
			Mailboxes: req.Mailboxes,
		}, nil
	}

	// Sidecar: mail-bodies tarball from /var/lib/stalwart so the
	// snapshot is self-contained for restore. Bodies are account-keyed
	// inside RocksDB; restore-side filters by account on apply.
	if err := writeMailBodiesTarball(ctx, filepath.Join(stagingDir, "bodies.tar")); err != nil {
		return nil, bkInternal("mail bodies tarball", err)
	}

	cfg, cerr := bkResticConfigWithPassword(req.RepoURL, req.CredentialsRef, req.PasswordFile, req.ExtraOptions)
	if cerr != nil {
		return nil, bkInternal("restic config", cerr)
	}
	c := backup.New(cfg)
	tags := backup.AccountBackupTags(req.JobID, req.UserID, req.ScheduleID, backup.StageMail)
	for _, mb := range req.Mailboxes {
		tags = append(tags, backup.Tag("mailbox="+mb))
	}
	summary, err := c.Backup(ctx, backup.BackupOpts{
		Paths: []string{stagingDir},
		Tags:  tags,
	})
	if err != nil {
		return nil, bkInternal("restic backup", err)
	}
	res := backupMailboxesResult{
		Mailboxes:  req.Mailboxes,
		SnapshotID: summary.SnapshotID,
		BytesAdded: summary.DataAdded,
		BytesTotal: summary.TotalBytesProcessed,
	}
	return res, nil
}

// stalwartAdminCreds returns the URL + Basic-auth credentials for the
// Stalwart admin endpoint. Reads the same files install.sh provisions:
//   - STALWART_RECOVERY_ADMIN=<user>:<token-b64> in
//     /etc/jabali-panel/stalwart.env  (NOT the API token; this is the
//     admin Basic-auth pair).
//   - admin URL is hard-coded to the loopback admin listener
//     127.0.0.1:18181 because that's what install.sh provisions.
//
// Returns empty strings on missing/parse failure; caller treats it as
// "skip mail stage" rather than fatal.
func stalwartAdminCreds() (url, user, pass string) {
	body, err := os.ReadFile("/etc/jabali-panel/stalwart.env")
	if err != nil {
		return "", "", ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "STALWART_RECOVERY_ADMIN=") {
			continue
		}
		val := strings.TrimPrefix(line, "STALWART_RECOVERY_ADMIN=")
		if i := strings.Index(val, ":"); i > 0 {
			return "http://127.0.0.1:18181", val[:i], val[i+1:]
		}
	}
	return "", "", ""
}

// writeMailBodiesTarball captures the Stalwart RocksDB store as a tar
// stream so the per-user mail snapshot is restoreable without a full
// system_backup. The store is shared across all accounts; restore-side
// applies the per-user plan on top of this base data.
func writeMailBodiesTarball(ctx context.Context, dst string) error {
	const dataDir = "/var/lib/stalwart"
	if _, err := os.Stat(dataDir); err != nil {
		return err
	}
	// Exclude RocksDB's debug log (LOG, LOG.old.*) and the in-process
	// LOCK sentinel — they're runtime artefacts, not data, and balloon
	// the snapshot by megabytes per backup. Everything else (.sst,
	// .blob, .log = WAL, MANIFEST, OPTIONS, CURRENT, IDENTITY) is
	// load-bearing for restore.
	cmd := exec.CommandContext(ctx, "tar", "-cf", dst,
		"--exclude=stalwart/LOG",
		"--exclude=stalwart/LOG.old.*",
		"--exclude=stalwart/LOCK",
		"-C", filepath.Dir(dataDir), filepath.Base(dataDir))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tar bodies: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func splitMailbox(mb string) (local, domain string, ok bool) {
	at := strings.LastIndex(mb, "@")
	if at <= 0 || at == len(mb)-1 {
		return "", "", false
	}
	return mb[:at], mb[at+1:], true
}

// stalwartActive returns true when systemctl reports the service active.
// Best-effort: if `systemctl` is missing or the call errors out, we
// assume Stalwart is up rather than skipping every mail backup.
func stalwartActive(ctx context.Context) bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return true
	}
	out, _ := exec.CommandContext(ctx, "systemctl", "is-active", "jabali-stalwart.service").Output()
	return strings.TrimSpace(string(out)) == "active"
}

func init() {
	Default.Register("backup.mailboxes", backupMailboxesHandler)
}
