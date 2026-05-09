// migration.import_mailboxes — push extracted Maildir messages into
// Stalwart via JMAP Blob/upload + Email/import (M35 cPanel restore
// mailboxes per-area writer; upgrades the panel-side observation
// stub at panel-api/internal/migrate/cpanel/restore_mail.go to
// real ingest).
//
// Per-mailbox workflow:
//  1. Resolve accountId by email (existing accountIDByEmail helper)
//  2. Resolve INBOX mailboxId via Mailbox/query (role=inbox)
//  3. For each .eml file in cur/ + new/:
//     a. POST raw bytes to /jmap/upload → blobId
//     b. Email/import with blobId + mailboxIds:{<inbox>:true} +
//        keywords:{$seen:true for cur/, none for new/} +
//        receivedAt parsed from Maildir filename
//  4. Record bytes + count in MailboxImportResult
//
// Idempotent on resume: a re-run will re-upload + re-import. Stalwart
// dedupes on Message-ID — duplicate imports become silent no-ops at
// the JMAP layer. We don't track per-message progress in
// migration_stages (would 10x the row count); operator sees per-
// mailbox count + bytes summary in the manifest_json warnings.
//
// SECURITY: src_dir is path-validated against /var/lib/jabali-
// migrations/ prefix (same as migration_import_home). Refuses any
// path outside the staging root.
package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

const (
	migrationMailboxTimeout = 4 * time.Hour
	migrationMailboxJMAPCallTimeout = 30 * time.Second
	// migrationMailboxMessageCap caps per-message size at 64 MiB.
	// Stalwart's default is 50 MiB; bumped slightly so a slightly-
	// over-default attachment doesn't fail the whole mailbox.
	migrationMailboxMessageCap = 64 << 20
)

type migrationImportMailboxesParams struct {
	JobID      string `json:"job_id"`
	SrcMailDir string `json:"src_mail_dir"` // /var/lib/jabali-migrations/<id>/extracted/cp/<u>/homedir/mail
}

type migrationImportMailboxesResult struct {
	MailboxesProcessed int      `json:"mailboxes_processed"`
	MessagesImported   int64    `json:"messages_imported"`
	MessagesSkipped    int64    `json:"messages_skipped"`
	BytesImported      int64    `json:"bytes_imported"`
	Skipped            []string `json:"skipped,omitempty"`
}

func init() {
	Default.Register("migration.import_mailboxes", migrationImportMailboxesHandler)
}

func migrationImportMailboxesHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var p migrationImportMailboxesParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "malformed JSON: " + err.Error(),
		}
	}
	if p.JobID == "" || p.SrcMailDir == "" {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "job_id, src_mail_dir required",
		}
	}
	srcAbs, err := filepath.Abs(p.SrcMailDir)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "src_mail_dir not absolute: " + err.Error(),
		}
	}
	if !strings.HasPrefix(srcAbs+"/", migrationStagingRoot+"/") {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("src_mail_dir must live under %s, got %q", migrationStagingRoot, srcAbs),
		}
	}

	subctx, cancel := context.WithTimeout(ctx, migrationMailboxTimeout)
	defer cancel()

	res := &migrationImportMailboxesResult{}

	// Layout: cp/<user>/homedir/mail/<domain>/<localpart>/{cur,new,tmp}/
	// SrcMailDir points at .../homedir/mail.
	domains, err := os.ReadDir(srcAbs)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("read mail root %s: %v", srcAbs, err),
		}
	}
	for _, dom := range domains {
		if !dom.IsDir() {
			continue
		}
		domPath := filepath.Join(srcAbs, dom.Name())
		users, err := os.ReadDir(domPath)
		if err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("read domain %s: %v", dom.Name(), err))
			continue
		}
		for _, u := range users {
			if !u.IsDir() {
				continue
			}
			userPath := filepath.Join(domPath, u.Name())
			maildirPath, ok := looksLikeMailMaildir(userPath)
			if !ok {
				continue
			}
			email := fmt.Sprintf("%s@%s", u.Name(), dom.Name())
			n, b, skipped, err := importOneMailbox(subctx, email, maildirPath)
			if err != nil {
				// Don't fail the whole job on one mailbox — record
				// + skip. Operator inspects manifest_json + can
				// re-run if needed.
				res.Skipped = append(res.Skipped, fmt.Sprintf("%s: %v", email, err))
				continue
			}
			res.MailboxesProcessed++
			res.MessagesImported += n
			res.BytesImported += b
			res.Skipped = append(res.Skipped, skipped...)
		}
	}
	return res, nil
}

// looksLikeMailMaildir checks for cur/ or new/ direct children
// (cpanel + Hestia layout: <local>/{cur,new,tmp}). When that
// fails, tries <local>/Maildir/{cur,new}/ — DA layout. Returns
// the path that contains cur+new (either userPath or
// userPath/Maildir) plus a bool indicating whether a Maildir-
// shaped tree was found.
func looksLikeMailMaildir(path string) (string, bool) {
	for _, marker := range []string{"cur", "new"} {
		if st, err := os.Stat(filepath.Join(path, marker)); err == nil && st.IsDir() {
			return path, true
		}
	}
	// DA: extra Maildir/ subdir.
	dapath := filepath.Join(path, "Maildir")
	for _, marker := range []string{"cur", "new"} {
		if st, err := os.Stat(filepath.Join(dapath, marker)); err == nil && st.IsDir() {
			return dapath, true
		}
	}
	return "", false
}

// importOneMailbox pushes every .eml-shaped message in cur/ + new/
// into the destination Stalwart account's INBOX. Returns
// (messages_imported, bytes_imported, skipped, error).
//
// Email/import is per-message because Stalwart's blob upload limit
// is per-blob, not per-batch. Pipelining 10-100 imports per JMAP
// call would be a follow-up optimisation; v1 sequential import is
// correct + bounded.
func importOneMailbox(ctx context.Context, destEmail, maildir string) (int64, int64, []string, error) {
	accountID, err := accountIDByEmail(ctx, destEmail)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("resolve account %q: %w", destEmail, err)
	}
	if accountID == "" {
		return 0, 0, nil, fmt.Errorf("destination account %q not found in Stalwart — run domain.email.enable first", destEmail)
	}
	inboxID, err := mailboxIDByRole(ctx, accountID, "inbox")
	if err != nil {
		return 0, 0, nil, fmt.Errorf("resolve inbox: %w", err)
	}

	var imported, bytes int64
	var skipped []string

	for _, sub := range []string{"cur", "new"} {
		dir := filepath.Join(maildir, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		seenFlag := sub == "cur" // cur/ messages are already-read in Maildir spec
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name())
			info, err := e.Info()
			if err != nil {
				skipped = append(skipped, fmt.Sprintf("stat %s: %v", path, err))
				continue
			}
			if info.Size() > migrationMailboxMessageCap {
				skipped = append(skipped, fmt.Sprintf("oversized:%s:%d", path, info.Size()))
				continue
			}
			n, err := importOneMessage(ctx, accountID, inboxID, path, info.Size(), seenFlag, info.ModTime())
			if err != nil {
				skipped = append(skipped, fmt.Sprintf("%s: %v", path, err))
				continue
			}
			imported++
			bytes += n
		}
	}
	return imported, bytes, skipped, nil
}

// mailboxIDByRole resolves the mailbox ID with the given role
// (e.g. "inbox") in the named account. Stalwart auto-creates an
// INBOX on account.create; we expect role=inbox to always return
// exactly one row.
func mailboxIDByRole(ctx context.Context, accountID, role string) (string, error) {
	var resp struct {
		IDs []string `json:"ids"`
	}
	if err := jmapCall(ctx, "Mailbox/query", map[string]any{
		"accountId": accountID,
		"filter":    map[string]any{"role": role},
		"limit":     1,
	}, &resp); err != nil {
		return "", err
	}
	if len(resp.IDs) == 0 {
		return "", fmt.Errorf("no mailbox with role=%q in account %s", role, accountID)
	}
	return resp.IDs[0], nil
}

// importOneMessage = blob upload + Email/import in two HTTP round-
// trips. Returns the bytes uploaded.
func importOneMessage(ctx context.Context, accountID, mailboxID, path string, size int64, seenFlag bool, receivedAt time.Time) (int64, error) {
	blobID, err := uploadBlob(ctx, accountID, path)
	if err != nil {
		return 0, fmt.Errorf("blob/upload: %w", err)
	}
	keywords := map[string]bool{}
	if seenFlag {
		keywords["$seen"] = true
	}
	receivedAtStr := receivedAt.UTC().Format(time.RFC3339)
	args := map[string]any{
		"accountId": accountID,
		"emails": map[string]any{
			"m0": map[string]any{
				"blobId":      blobID,
				"mailboxIds":  map[string]bool{mailboxID: true},
				"keywords":    keywords,
				"receivedAt":  receivedAtStr,
			},
		},
	}
	var resp struct {
		Created   map[string]json.RawMessage `json:"created"`
		NotCreated map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description,omitempty"`
		} `json:"notCreated"`
	}
	if err := jmapCallWith(ctx, "urn:ietf:params:jmap:mail", "Email/import", args, &resp); err != nil {
		return size, err
	}
	if nc, ok := resp.NotCreated["m0"]; ok {
		// Common notCreated reasons: alreadyExists (Stalwart
		// dedup on Message-ID), tooLarge, invalidMailbox.
		// alreadyExists is non-fatal; treat as success.
		if nc.Type == "alreadyExists" {
			return size, nil
		}
		return size, fmt.Errorf("Email/import notCreated: %s: %s", nc.Type, nc.Description)
	}
	return size, nil
}

// uploadBlob streams the file at `path` to Stalwart's /jmap/upload/<accountId>
// endpoint. Returns the produced blobId.
func uploadBlob(ctx context.Context, accountID, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	url := stalwartAdminURLFunc() + "/jmap/upload/" + accountID
	subctx, cancel := context.WithTimeout(ctx, migrationMailboxJMAPCallTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(subctx, http.MethodPost, url, f)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "message/rfc822")
	token, err := stalwartAdminTokenFunc()
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(jmapAdminUser, token)

	resp, err := stalwartHTTPClientFunc().Do(req)
	if err != nil {
		return "", fmt.Errorf("post upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("upload HTTP %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		BlobID string `json:"blobId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode upload response: %w", err)
	}
	if out.BlobID == "" {
		return "", errors.New("upload returned empty blobId")
	}
	return out.BlobID, nil
}

// jmapCallWith is jmapCall with an extra capability URN appended to
// the `using` array — Email/import requires
// urn:ietf:params:jmap:mail beyond the base set jmapCall sends.
func jmapCallWith(ctx context.Context, extraCap, method string, args any, out any) error {
	body := jmapRequestBody{
		Using: append(append([]string{}, jmapUsing...), extraCap),
		MethodCalls: []jmapMethodCall{
			{Name: method, Args: args, CallID: "c0"},
		},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal jmap request: %w", err)
	}
	url := stalwartAdminURLFunc() + jmapAPIPath
	subctx, cancel := context.WithTimeout(ctx, migrationMailboxJMAPCallTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(subctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	token, err := stalwartAdminTokenFunc()
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(jmapAdminUser, token)

	resp, err := stalwartHTTPClientFunc().Do(req)
	if err != nil {
		return fmt.Errorf("jmap call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("jmap HTTP %d", resp.StatusCode)
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var parsed jmapResponseBody
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return fmt.Errorf("unparseable response: %w", err)
	}
	if len(parsed.MethodResponses) != 1 {
		return fmt.Errorf("jmap returned %d method responses, want 1", len(parsed.MethodResponses))
	}
	mr := parsed.MethodResponses[0]
	rawArgs, ok := mr.Args.(json.RawMessage)
	if !ok {
		// jmapMethodCall.Args is decoded as json.RawMessage by
		// the UnmarshalJSON in the jmap client (mailbox_jmap.go).
		// On the rare path where the type slipped, marshal the
		// `any` back to JSON so the rest of this function can
		// branch on the contents.
		b, mErr := json.Marshal(mr.Args)
		if mErr != nil {
			return fmt.Errorf("jmap response args not RawMessage and remarshal failed: %w", mErr)
		}
		rawArgs = b
	}
	if mr.Name == "error" {
		return fmt.Errorf("jmap error: %s", string(rawArgs))
	}
	if out != nil {
		return json.Unmarshal(rawArgs, out)
	}
	return nil
}
