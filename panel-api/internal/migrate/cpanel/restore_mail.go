package cpanel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MailImportResult is returned to the restore-stage caller.
type MailImportResult struct {
	// MaildirsFound is the per-mailbox count (one per
	// home/mail/<domain>/<user>/Maildir/ tree).
	MaildirsFound int
	// MessagesFound is the total .eml + Maildir-flag-suffixed
	// message count across every found Maildir.
	MessagesFound int
	// BytesFound is the sum of stat'd message sizes — drives the
	// 'how much mail will need migrating' projection.
	BytesFound int64
	// Skipped is the runner-visible warnings list, prefixed with
	// 'mailbox_pending_manual:' per Maildir + a final one-liner
	// directing the operator to the runbook §2.6.
	Skipped []string
}

// ImportMailboxes walks the homedir/mail/ tree of the parsed cpmove
// tarball, counts mailboxes + messages + bytes, and records every
// mailbox as a 'pending_manual' warning. It does NOT push messages
// to Stalwart — JMAP push is multi-session work tracked under the
// M35 follow-up list.
//
// Why ship this stub now: the 'mailboxes are deferred' note in the
// runbook is more actionable when the operator can see exact paths
// + counts in the post-migration manifest_json. An empty mailbox
// import (no observation surface at all) leaves the operator
// guessing whether a mail-only account migrated correctly.
//
// When the JMAP-push milestone lands, this function transitions to
// pushing each .eml via Email/import. Wire shape (MailImportResult)
// stays compatible — Skipped slice grows / shrinks based on
// per-message push outcomes.
func ImportMailboxes(_ context.Context, parsed *ParsedTarball) (*MailImportResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("ImportMailboxes: parsed nil")
	}
	res := &MailImportResult{}
	if parsed.HomeDir == "" {
		// No homedir → no Maildir tree. Not fatal; record + return.
		res.Skipped = append(res.Skipped, "mailbox_skip:no_homedir_in_tarball")
		return res, nil
	}

	mailRoot := filepath.Join(parsed.HomeDir, "mail")
	if !existsDir(mailRoot) {
		res.Skipped = append(res.Skipped, "mailbox_skip:no_mail_subtree")
		return res, nil
	}

	// cPanel Maildir layout: home/mail/<domain>/<localpart>/{cur,new,tmp}/
	// We walk to depth 2 (domain → localpart) and look for Maildir
	// markers (any of cur/, new/, tmp/) inside.
	domains, err := os.ReadDir(mailRoot)
	if err != nil {
		res.Skipped = append(res.Skipped, fmt.Sprintf("mail_read_root: %v", err))
		return res, nil
	}
	for _, dom := range domains {
		if !dom.IsDir() {
			continue
		}
		domPath := filepath.Join(mailRoot, dom.Name())
		users, err := os.ReadDir(domPath)
		if err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("mail_read_domain %s: %v", dom.Name(), err))
			continue
		}
		for _, u := range users {
			if !u.IsDir() {
				continue
			}
			userPath := filepath.Join(domPath, u.Name())
			if !looksLikeMaildir(userPath) {
				continue
			}
			res.MaildirsFound++
			email := fmt.Sprintf("%s@%s", u.Name(), dom.Name())
			n, b := countMaildirMessages(userPath)
			res.MessagesFound += n
			res.BytesFound += b
			res.Skipped = append(res.Skipped, fmt.Sprintf(
				"mailbox_pending_manual:%s msgs=%d bytes=%d path=%s",
				email, n, b, userPath))
		}
	}
	if res.MaildirsFound > 0 {
		res.Skipped = append(res.Skipped,
			"mailboxes_pending_manual:see runbook §2.6 — JMAP push not yet wired; "+
				"recommended path is keep source SMTP active + cut over DNS MX after drain")
	}
	return res, nil
}

// looksLikeMaildir checks for the Maildir-spec directory markers.
// Conservative: requires at least 'cur' or 'new' as direct children.
func looksLikeMaildir(path string) bool {
	for _, marker := range []string{"cur", "new"} {
		if existsDir(filepath.Join(path, marker)) {
			return true
		}
	}
	return false
}

// countMaildirMessages walks cur/, new/, tmp/ inside a Maildir +
// sums file count + bytes. Skips directories silently — Maildir's
// spec says cur/new/tmp contain only regular files.
func countMaildirMessages(maildir string) (count int, bytes int64) {
	for _, sub := range []string{"cur", "new", "tmp"} {
		dir := filepath.Join(maildir, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			// Basic Maildir filename sanity: should contain a
			// flag delimiter ':' OR be a unique name. We accept
			// any regular file rather than over-filter, since
			// some clients store quirky names.
			_ = strings.Contains(e.Name(), ":")
			count++
			bytes += info.Size()
		}
	}
	return count, bytes
}
