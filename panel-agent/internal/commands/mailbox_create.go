package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// mailboxCreateParams is the request shape for mailbox.create.
//
// In v0.16 (ADR-0045) this is an agent-side no-op with respect to
// Stalwart: the panel's SQL write to jabali_panel.mailboxes is
// authoritative, and Stalwart's SqlDirectory re-reads the row on the
// mailbox owner's first auth (no TTL window, no cache to invalidate).
//
// We keep the command registered and the wire contract ({id, email} ->
// {ok: true}) unchanged so the panel-side pipeline doesn't need a
// conditional for whether to invoke the agent; it always does, and the
// agent acks with a structured "no action needed" result.
//
// Param validation still runs — that's defence in depth against a
// malformed request sneaking past the panel-API layer.
type mailboxCreateParams struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

func mailboxCreateHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p mailboxCreateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.ID == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "id parameter required"}
	}
	if _, err := requireEmail(p.Email); err != nil {
		return nil, err
	}
	// Stalwart cache primer: a JMAP accountIDByEmail lookup forces
	// SqlDirectory to query mailboxes for this email NOW. ADR-0045
	// says the directory pulls on first auth without TTL, but QA
	// surfaced UI-created mailboxes failing IMAP auth until a CLI
	// 'jabali mailbox passwd' "fixed" them — suggesting Stalwart
	// caches negative-lookups longer than ADR-0045 assumed. A
	// dummy lookup post-DB-insert primes the directory; any
	// negative-cache entry gets superseded by a positive one.
	//
	// Errors swallowed: the row is already authoritative.
	// accountIDByEmail returning empty (no Stalwart principal yet)
	// is expected for a fresh mailbox.
	_, _ = accountIDByEmail(ctx, p.Email)
	return okBody{Ok: true}, nil
}

func init() {
	Default.Register("mailbox.create", mailboxCreateHandler)
}
