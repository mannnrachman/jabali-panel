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
	if _, err := exec.LookPath("stalwart-cli"); err != nil {
		return backupMailboxesResult{
			Skipped:   true,
			Reason:    "stalwart_cli_missing",
			Mailboxes: req.Mailboxes,
		}, nil
	}

	stagingDir := filepath.Join(backupStagingRoot, req.JobID, "mail")
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return nil, bkInternal("mkdir staging", err)
	}
	defer os.RemoveAll(stagingDir)

	res := backupMailboxesResult{Mailboxes: make([]string, 0, len(req.Mailboxes))}
	for _, mb := range req.Mailboxes {
		dst := filepath.Join(stagingDir, mb+".mbox")
		cmd := exec.CommandContext(ctx, "stalwart-cli",
			"account", "export", mb,
			"--format=mbox",
			"--output="+dst,
		)
		if err := cmd.Run(); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", mb, err))
			continue
		}
		res.Mailboxes = append(res.Mailboxes, mb)
	}

	if len(res.Mailboxes) == 0 {
		// Every mailbox failed; do not produce an empty snapshot.
		return res, nil
	}

	c := backup.New(backup.DefaultConfig())
	tags := backup.AccountBackupTags(req.JobID, req.UserID, backup.StageMail)
	summary, err := c.Backup(ctx, backup.BackupOpts{
		Paths: []string{stagingDir},
		Tags:  tags,
	})
	if err != nil {
		return nil, bkInternal("restic backup", err)
	}
	res.SnapshotID = summary.SnapshotID
	res.BytesAdded = summary.DataAdded
	res.BytesTotal = summary.TotalBytesProcessed
	return res, nil
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
