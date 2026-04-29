package mailscan

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ---- Principal/query + Principal/get ----

// Principal is the subset of a JMAP Principal we use to enumerate
// per-tenant accounts. The principal id is the same value used as
// `accountId` on Mailbox/Email/Blob calls.
type Principal struct {
	ID    string `json:"id"`
	Type  string `json:"type"`  // "individual" / "group" / "resource" / "location"
	Name  string `json:"name"`
	Email string `json:"email"`
}

type principalQueryResp struct {
	IDs []string `json:"ids"`
}

type principalGetResp struct {
	List []Principal `json:"list"`
}

// ListPrincipals returns every principal admin can see. callerAccountID
// is admin's own account ID (from /jmap/session.primaryAccounts) — JMAP
// requires it on the request envelope even for principal-scoped calls.
// Filters down to type=individual to skip groups/resources.
func (c *Client) ListPrincipals(ctx context.Context, callerAccountID string) ([]Principal, error) {
	resp, err := c.call(ctx,
		jmapMethodCall{
			Name:   "Principal/query",
			Args:   map[string]any{"accountId": callerAccountID},
			CallID: "c1",
		},
		jmapMethodCall{
			Name: "Principal/get",
			Args: map[string]any{
				"accountId": callerAccountID,
				"#ids": map[string]any{
					"resultOf": "c1",
					"name":     "Principal/query",
					"path":     "/ids",
				},
			},
			CallID: "c2",
		},
	)
	if err != nil {
		return nil, err
	}
	var out []Principal
	for _, mr := range resp.MethodResponses {
		if mr.Name != "Principal/get" {
			continue
		}
		var pg principalGetResp
		if err := json.Unmarshal(mr.Args, &pg); err != nil {
			return nil, fmt.Errorf("decode Principal/get: %w", err)
		}
		for _, p := range pg.List {
			if p.Type == "individual" {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

// CallerAccountID returns admin's own primaryAccounts[mail] entry. For
// the simple admin-token deployment, the username equals the admin
// account name and the principal-query call uses this as accountId.
func (c *Client) CallerAccountID() string {
	// /jmap/session.accounts is keyed by accountId; we don't store the
	// full map but the username is enough for primary-account lookup
	// in single-tenant Stalwart deployments. Future: parse
	// primaryAccounts on discover() and expose it here.
	return c.username
}

// ---- Mailbox/query + Mailbox/get + Mailbox/set ----

type Mailbox struct {
	ID       string  `json:"id,omitempty"`
	Name     string  `json:"name"`
	Role     *string `json:"role,omitempty"`
	ParentID *string `json:"parentId,omitempty"`
}

type mailboxGetResp struct {
	List []Mailbox `json:"list"`
}

// MailboxQuery filter — narrows to Inbox by default. When scanAllFolders
// is true, callers omit the filter and walk every mailbox except known
// noise folders (Spam, Sent, Trash) which the orchestrator screens.
type mailboxQueryFilter struct {
	Role string `json:"role,omitempty"`
}

type mailboxQueryResp struct {
	IDs []string `json:"ids"`
}

// QueryInbox returns the inbox mailbox id for the given account, or
// empty string if none (account never logged in). Stalwart auto-creates
// the inbox on first JMAP read but admin enumeration may pre-empt it.
func (c *Client) QueryInbox(ctx context.Context, accountID string) (string, error) {
	var resp mailboxQueryResp
	if err := c.callSingle(ctx, "Mailbox/query", map[string]any{
		"accountId": accountID,
		"filter":    mailboxQueryFilter{Role: "inbox"},
	}, &resp); err != nil {
		return "", err
	}
	if len(resp.IDs) == 0 {
		return "", nil
	}
	return resp.IDs[0], nil
}

// QueryAllScannableFolders returns every mailbox id except Sent/Trash/
// Drafts/Junk/Spam. Used when malware_settings.mail_scan_all_folders is
// on. Junk + Spam are excluded because Stalwart already classified them
// + a hit there is not actionable for the operator.
func (c *Client) QueryAllScannableFolders(ctx context.Context, accountID string) ([]string, error) {
	var qr mailboxQueryResp
	if err := c.callSingle(ctx, "Mailbox/query", map[string]any{
		"accountId": accountID,
		"limit":     500,
	}, &qr); err != nil {
		return nil, err
	}
	if len(qr.IDs) == 0 {
		return nil, nil
	}
	var gr mailboxGetResp
	if err := c.callSingle(ctx, "Mailbox/get", map[string]any{
		"accountId":  accountID,
		"ids":        qr.IDs,
		"properties": []string{"id", "name", "role"},
	}, &gr); err != nil {
		return nil, err
	}
	skip := map[string]bool{"sent": true, "trash": true, "drafts": true, "junk": true, "spam": true}
	out := make([]string, 0, len(gr.List))
	for _, m := range gr.List {
		role := ""
		if m.Role != nil {
			role = *m.Role
		}
		if skip[role] {
			continue
		}
		out = append(out, m.ID)
	}
	return out, nil
}

// MailboxExists returns true iff the given mailbox id exists in the
// account. Used to revalidate cached quarantine_mailbox ids before
// reuse — a user who manually deleted "Malware" causes our cache to
// hold a dead id.
func (c *Client) MailboxExists(ctx context.Context, accountID, mailboxID string) (bool, error) {
	var resp mailboxGetResp
	if err := c.callSingle(ctx, "Mailbox/get", map[string]any{
		"accountId":  accountID,
		"ids":        []string{mailboxID},
		"properties": []string{"id"},
	}, &resp); err != nil {
		return false, err
	}
	return len(resp.List) == 1, nil
}

type mailboxSetCreateArgs struct {
	AccountID string                 `json:"accountId"`
	Create    map[string]Mailbox     `json:"create"`
	Update    map[string]any         `json:"update,omitempty"`
}

type mailboxSetResp struct {
	Created    map[string]Mailbox `json:"created"`
	NotCreated map[string]any     `json:"notCreated"`
}

// EnsureQuarantineMailbox returns the id of the per-account "Malware"
// mailbox, creating it if missing. Role is intentionally null — Junk
// is reserved for spam classification, this is malware-detection
// quarantine which has different semantics for the user.
func (c *Client) EnsureQuarantineMailbox(ctx context.Context, accountID, name string) (string, error) {
	if name == "" {
		name = "Malware"
	}
	// Try to find an existing mailbox by name first. Stalwart name uniqueness
	// is per-parent so a top-level Malware folder collides only with another
	// top-level Malware folder.
	var qr mailboxQueryResp
	if err := c.callSingle(ctx, "Mailbox/query", map[string]any{
		"accountId": accountID,
		"filter":    map[string]any{"name": name, "hasAnyRole": false},
		"limit":     1,
	}, &qr); err == nil && len(qr.IDs) == 1 {
		return qr.IDs[0], nil
	}
	// Create.
	var resp mailboxSetResp
	if err := c.callSingle(ctx, "Mailbox/set", mailboxSetCreateArgs{
		AccountID: accountID,
		Create: map[string]Mailbox{
			"new1": {Name: name},
		},
	}, &resp); err != nil {
		return "", err
	}
	if m, ok := resp.Created["new1"]; ok && m.ID != "" {
		return m.ID, nil
	}
	if e, ok := resp.NotCreated["new1"]; ok {
		return "", fmt.Errorf("create %q failed: %v", name, e)
	}
	return "", fmt.Errorf("Mailbox/set returned no id for %q", name)
}

// ---- Email/query + Email/get + Email/set (move) ----

// EmailHeader is a minimal subject/from extraction for ingest event
// metadata. JMAP returns headers as [name, value] tuples — we resolve
// from + subject directly via `properties` so we don't have to walk.
type EmailMetadata struct {
	ID           string         `json:"id"`
	ReceivedAt   time.Time      `json:"receivedAt"`
	Subject      string         `json:"subject"`
	From         []EmailAddress `json:"from"`
	HasAttachment bool          `json:"hasAttachment"`
	Attachments  []EmailPart    `json:"attachments"`
	MailboxIDs   map[string]bool `json:"mailboxIds"`
}

type EmailAddress struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type EmailPart struct {
	PartID string `json:"partId"`
	BlobID string `json:"blobId"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
}

type emailQueryResp struct {
	IDs []string `json:"ids"`
}

type emailGetResp struct {
	List []EmailMetadata `json:"list"`
}

// QueryNewEmails returns email ids in mailboxID with receivedAt strictly
// greater than `since`. Sorted ASC so cursor advances monotonically.
// limit caps per-tick work; the caller sets it from the per-tick budget.
func (c *Client) QueryNewEmails(ctx context.Context, accountID, mailboxID string, since *time.Time, limit int) ([]string, error) {
	filter := map[string]any{"inMailbox": mailboxID}
	if since != nil {
		filter["after"] = since.UTC().Format(time.RFC3339)
	}
	var resp emailQueryResp
	if err := c.callSingle(ctx, "Email/query", map[string]any{
		"accountId": accountID,
		"filter":    filter,
		"sort":      []map[string]any{{"property": "receivedAt", "isAscending": true}},
		"limit":     limit,
	}, &resp); err != nil {
		return nil, err
	}
	return resp.IDs, nil
}

// GetEmailsWithAttachments fetches metadata + attachment list for a
// batch of email ids. bodyProperties trims the per-part payload to the
// fields the scanner needs.
func (c *Client) GetEmailsWithAttachments(ctx context.Context, accountID string, ids []string) ([]EmailMetadata, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var resp emailGetResp
	if err := c.callSingle(ctx, "Email/get", map[string]any{
		"accountId": accountID,
		"ids":       ids,
		"properties": []string{
			"id", "receivedAt", "subject", "from",
			"hasAttachment", "attachments", "mailboxIds",
		},
		"bodyProperties": []string{"partId", "blobId", "name", "type", "size"},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.List, nil
}

// MoveEmailToMailbox updates an email's mailboxIds map to put it
// exclusively in `targetMailboxID`. Stalwart Email/set with mailboxIds
// replaces the whole set (per JMAP §4.4).
func (c *Client) MoveEmailToMailbox(ctx context.Context, accountID, emailID, targetMailboxID string) error {
	var resp struct {
		Updated    map[string]any `json:"updated"`
		NotUpdated map[string]any `json:"notUpdated"`
	}
	if err := c.callSingle(ctx, "Email/set", map[string]any{
		"accountId": accountID,
		"update": map[string]any{
			emailID: map[string]any{
				"mailboxIds": map[string]bool{targetMailboxID: true},
			},
		},
	}, &resp); err != nil {
		return err
	}
	if e, ok := resp.NotUpdated[emailID]; ok {
		return fmt.Errorf("Email/set move %s: %v", emailID, e)
	}
	return nil
}

// DownloadAttachment is the public Blob fetch entrypoint. Wraps the
// downloadBlob helper with the size cap from settings.
func (c *Client) DownloadAttachment(ctx context.Context, accountID string, p EmailPart, maxBytes int64) ([]byte, bool, error) {
	return c.downloadBlob(ctx, accountID, p.BlobID, p.Name, p.Type, maxBytes)
}
