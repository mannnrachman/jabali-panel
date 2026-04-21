package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// mailboxSetQuotaParams is the request shape for mailbox.set_quota.
//
// Panel has already updated mailboxes.quota_bytes. Quota enforcement is
// read-at-auth inside Stalwart (via the SQL directory's query.name
// projection), so cache invalidation is what makes the new ceiling
// effective for already-authenticated sessions.
type mailboxSetQuotaParams struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	QuotaBytes uint64 `json:"quota_bytes"`
}

// mailboxSetQuotaResponse echoes the quota back so the panel's inline-
// best-effort handler can log the applied value.
type mailboxSetQuotaResponse struct {
	Ok         bool   `json:"ok"`
	QuotaBytes uint64 `json:"quota_bytes"`
}

func mailboxSetQuotaHandler(ctx context.Context, params json.RawMessage) (any, error) {
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
	email, err := requireEmail(p.Email)
	if err != nil {
		return nil, err
	}
	// Zero quota is allowed ("disable mailbox without deletion"); negative
	// is caught by the uint64 type. Upper bound is whatever MariaDB's
	// BIGINT UNSIGNED accepts; no panel-side semantic cap in v1.

	if err := invalidateStalwartPrincipal(ctx, email); err != nil {
		return nil, err
	}
	return mailboxSetQuotaResponse{Ok: true, QuotaBytes: p.QuotaBytes}, nil
}

func init() {
	Default.Register("mailbox.set_quota", mailboxSetQuotaHandler)
}
