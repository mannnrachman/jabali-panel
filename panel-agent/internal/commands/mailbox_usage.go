package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// mailboxUsageParams is the request shape for mailbox.usage.
//
// Unlike the four no-op commands, this one READS from Stalwart's
// registry via JMAP — it's how the reconciler's 5-minute sampler
// keeps mailboxes.last_usage_* columns fresh for the UI's progress
// bar.
type mailboxUsageParams struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// mailboxUsageResponse is the wire contract consumed by the reconciler.
// Panel maps used_bytes -> mailboxes.last_usage_bytes + updates
// last_usage_at = wall-clock now() (not the last_used_at Stalwart
// returns — that's "when the principal was last authed", which is
// different from "when we last polled").
//
// Wire contract unchanged from Step 3's v0.15 draft — only the agent's
// internal impl pivoted from REST to JMAP.
type mailboxUsageResponse struct {
	UsedBytes    uint64 `json:"used_bytes"`
	MessageCount uint64 `json:"message_count"`
	// LastUsedAt is Stalwart's view of the last auth/access timestamp.
	// Empty string if Stalwart has never seen an auth for this
	// principal (fresh mailbox, never synced into the registry).
	LastUsedAt string `json:"last_used_at,omitempty"`
}

func mailboxUsageHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p mailboxUsageParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.ID == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "id parameter required"}
	}
	email, err := requireEmail(p.Email)
	if err != nil {
		return nil, err
	}

	// Step 1: resolve the registry Account id. Lazy-sync means the
	// record won't exist until the mailbox owner has authenticated at
	// least once; for a brand-new mailbox the sampler sees no usage,
	// which maps to "all zeros" — not an error.
	accountID, err := accountIDByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if accountID == "" {
		return mailboxUsageResponse{}, nil
	}

	// Step 2: read quota + message-count + last-auth timestamp.
	info, err := accountQuota(ctx, accountID)
	if err != nil {
		return nil, err
	}

	var lastUsed string
	if info.LastAuthAt != nil && !info.LastAuthAt.IsZero() {
		lastUsed = info.LastAuthAt.UTC().Format(time.RFC3339)
	}
	return mailboxUsageResponse{
		UsedBytes:    info.QuotaUsed,
		MessageCount: info.MessageCount,
		LastUsedAt:   lastUsed,
	}, nil
}

func init() {
	Default.Register("mailbox.usage", mailboxUsageHandler)
}
