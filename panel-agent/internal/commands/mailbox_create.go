package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// mailboxCreateParams is the request shape for mailbox.create.
//
// The panel has already written the mailboxes row at the moment this
// command fires. Our job is just to invalidate Stalwart's directory
// cache so the account is reachable on the very next auth instead of
// waiting for the default 60-second TTL.
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
	Default.Register("mailbox.create", mailboxCreateHandler)
}
