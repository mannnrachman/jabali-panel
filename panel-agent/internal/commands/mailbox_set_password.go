package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// mailboxSetPasswordParams is the request shape for mailbox.set_password.
//
// NB: this command does NOT carry the new password — the panel has already
// bcrypted it and written mailboxes.password_hash before calling us. All
// we do is evict Stalwart's directory-cache entry so the NEXT auth
// re-reads the hash from MariaDB. A stale cached entry would keep
// accepting the OLD password until the TTL expires.
//
// Plaintext never reaches the agent. That's the whole point of the post-
// review password model in ADR-0042 + plan §1 (two-column -> one-column
// bcrypt-only).
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
	Default.Register("mailbox.set_password", mailboxSetPasswordHandler)
}
