package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// mailboxSetQuotaParams is the request shape for mailbox.set_quota.
//
// Agent-side no-op against Stalwart in v0.16 (ADR-0045): panel has
// written the new quota_bytes to jabali_panel.mailboxes, and Stalwart's
// SqlDirectory re-queries the row on the next auth (no TTL cache).
// The new ceiling takes effect for the next session.
//
// Mid-session quota tightening: an already-authenticated user retains
// their existing AccessToken; new mail delivered within the token's
// lifetime uses the registry's synced ceiling (which is stale until the
// next auth). For v1 this window is acceptable; the runbook notes that
// operators wanting immediate enforcement can bounce the user's session
// via the webadmin.
type mailboxSetQuotaParams struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	QuotaBytes uint64 `json:"quota_bytes"`
}

// mailboxSetQuotaResponse echoes the quota back so the panel's inline
// handler can log the applied value.
type mailboxSetQuotaResponse struct {
	Ok         bool   `json:"ok"`
	QuotaBytes uint64 `json:"quota_bytes"`
}

func mailboxSetQuotaHandler(_ context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p mailboxSetQuotaParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.ID == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "id parameter required"}
	}
	if _, err := requireEmail(p.Email); err != nil {
		return nil, err
	}
	// Zero quota is allowed ("disable mailbox without deletion"); negative
	// is caught by the uint64 type. Upper bound is MariaDB's BIGINT UNSIGNED.
	return mailboxSetQuotaResponse{Ok: true, QuotaBytes: p.QuotaBytes}, nil
}

func init() {
	Default.Register("mailbox.set_quota", mailboxSetQuotaHandler)
}
