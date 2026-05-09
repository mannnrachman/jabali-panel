package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// mailboxSetPasswordParams is the request shape for mailbox.set_password.
//
// NB: this command does NOT carry the new password — the panel has
// already bcrypted it and written mailboxes.password_hash before calling
// us. In v0.16 the agent is a Stalwart-side no-op (ADR-0045): Stalwart's
// SqlDirectory re-reads the hash on the next auth attempt, so the new
// password is effective immediately for new sessions.
//
// Plaintext never reaches the agent. That's the whole point of the
// post-review password model in ADR-0042 + plan §1 (two-column ->
// one-column bcrypt-only).
//
// Mid-session note: existing AccessTokens are unaffected — the password
// change doesn't revoke active sessions. That's standard session
// behavior. Forced logout is a runbook escape-hatch via webadmin.
type mailboxSetPasswordParams struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

func mailboxSetPasswordHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p mailboxSetPasswordParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.ID == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "id parameter required"}
	}
	if _, err := requireEmail(p.Email); err != nil {
		return nil, err
	}
	// Ensure the account is in Stalwart's JMAP registry (covers
	// mailboxes created pre-fix that have never authenticated).
	// Best-effort; DB row is authoritative (ADR-0045).
	_ = accountEnsureInRegistry(ctx, p.Email)
	return okBody{Ok: true}, nil
}

func init() {
	Default.Register("mailbox.set_password", mailboxSetPasswordHandler)
}
