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
// Unlike the four cache-invalidate commands this one READS from Stalwart
// — it's how the reconciler's 5-minute sampler keeps mailboxes.last_usage_*
// columns fresh so the UI's progress bar has something to show.
type mailboxUsageParams struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// mailboxUsageResponse is the wire contract consumed by the reconciler.
// Panel maps used_bytes -> mailboxes.last_usage_bytes + updates
// last_usage_at = wall-clock now() (not the last_used_at Stalwart
// returns — that's "when the principal was last authed", which is
// different from "when we last polled").
type mailboxUsageResponse struct {
	UsedBytes    uint64 `json:"used_bytes"`
	MessageCount uint64 `json:"message_count"`
	// LastUsedAt is Stalwart's view of the last auth/access timestamp
	// (not the sampler's poll time). RFC 3339. Empty string if Stalwart
	// has never seen an auth for this principal (fresh mailbox).
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

	info, err := getStalwartPrincipalQuota(ctx, email)
	if err != nil {
		return nil, err
	}

	var lastUsed string
	if info.LastUsedAt != nil && !info.LastUsedAt.IsZero() {
		lastUsed = info.LastUsedAt.UTC().Format(time.RFC3339)
	}
	return mailboxUsageResponse{
		UsedBytes:    info.UsedBytes,
		MessageCount: info.MessageCount,
		LastUsedAt:   lastUsed,
	}, nil
}

func init() {
	Default.Register("mailbox.usage", mailboxUsageHandler)
}
