package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// domainCatchallClearParams is the request shape for domain.catchall_clear.
//
// Clears the catch-all address for a domain by setting catchAllAddress
// to null via x:Domain/set.
type domainCatchallClearParams struct {
	DomainID   string `json:"domain_id"`
	DomainName string `json:"domain_name"`
}

func domainCatchallClearHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p domainCatchallClearParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.DomainName == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "domain_name required"}
	}

	stalwartID, err := domainIDByName(ctx, p.DomainName)
	if err != nil {
		return nil, err
	}
	if stalwartID == "" {
		// Nothing to clear — treat as success (idempotent).
		return okBody{Ok: true}, nil
	}

	// Clear the domain's catchAllAddress by setting it to null
	if err := updateDomainCatchall(ctx, stalwartID, ""); err != nil {
		return nil, err
	}

	return okBody{Ok: true}, nil
}

func init() {
	Default.Register("domain.catchall_clear", domainCatchallClearHandler)
}
