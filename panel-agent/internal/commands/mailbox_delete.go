package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// mailboxDeleteParams is the request shape for mailbox.delete.
//
// Panel has already removed the mailboxes row. Same as mailbox.create:
// invalidate Stalwart's directory cache so any in-flight auth for this
// address fails immediately instead of succeeding until the TTL expires.
//
// Note: this command does NOT touch Stalwart's mail data (RocksDB blobs
// for the deleted user). Stalwart garbage-collects orphan principals on
// its own schedule. If the operator needs immediate reclamation they run
// `stalwart-cli purge-principal` manually — documented in the runbook.
type mailboxDeleteParams struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

func mailboxDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p mailboxDeleteParams
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

	if err := invalidateStalwartPrincipal(ctx, email); err != nil {
		return nil, err
	}
	return okBody{Ok: true}, nil
}

func init() {
	Default.Register("mailbox.delete", mailboxDeleteHandler)
}
