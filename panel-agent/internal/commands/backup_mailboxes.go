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
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
)

type backupMailboxesParams struct {
	JobID     string   `json:"job_id"`
	UserID    string   `json:"user_id"`
	Username  string   `json:"username"`
	Mailboxes []string `json:"mailboxes"`
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

	// v1 strategy: stream the entire Stalwart data dir (RocksDB) into
	// a single per-user snapshot, tagged with all mailbox addresses
	// the user owns so the restore-side knows which accounts to
	// recreate. Mail is account-keyed inside the RocksDB store and
	// can't be cheaply per-mailbox-extracted offline; the previous
	// `stalwart-cli account export` subcommand was removed in
	// Stalwart 0.16 which is why this stage was producing exit 2 on
	// every mailbox. Snapshotting the whole store still gives us the
	// data we need for restore (the same store is also captured in
	// system_backup but having it tagged per-user makes per-account
	// restore from an account_backup self-contained).
	stalwartDataDir := "/var/lib/stalwart"
	if _, err := os.Stat(stalwartDataDir); err != nil {
		return backupMailboxesResult{
			Skipped:   true,
			Reason:    "stalwart_data_dir_missing",
			Mailboxes: req.Mailboxes,
		}, nil
	}

	c := backup.New(backup.DefaultConfig())
	tags := backup.AccountBackupTags(req.JobID, req.UserID, backup.StageMail)
	for _, mb := range req.Mailboxes {
		tags = append(tags, backup.Tag("mailbox="+mb))
	}
	summary, err := c.Backup(ctx, backup.BackupOpts{
		Paths: []string{stalwartDataDir},
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

// backupStagingRoot is no longer used by the mailbox stage but kept
// as a package symbol so other backup commands can share the location.
var _ = backupStagingRoot

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
