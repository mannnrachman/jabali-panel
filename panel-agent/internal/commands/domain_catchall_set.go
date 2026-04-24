package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// domainCatchallSetParams is the request shape for domain.catchall_set.
//
// Sets the catch-all address for a domain via x:Domain/set with a
// catchAllAddress patch. Stalwart delivers mail to non-existent
// recipients at the domain to this email address.
//
// DomainName is authoritative — the agent resolves it to Stalwart's
// server-assigned Domain id. DomainID is accepted for backward-compat
// with the M6.5 first-ship callers but is ignored: the jabali DB ULID
// is not Stalwart's id, and passing it as-is made every /set call fail
// with "domain not found".
type domainCatchallSetParams struct {
	DomainID   string `json:"domain_id"`
	DomainName string `json:"domain_name"`
	Target     string `json:"target"` // email address or empty to clear
}

// domainCatchallSetResponse echoes the new target so the panel can log
// the applied value.
type domainCatchallSetResponse struct {
	Ok     bool   `json:"ok"`
	Target string `json:"target"`
}

func domainCatchallSetHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p domainCatchallSetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.DomainName == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "domain_name required"}
	}

	// Validate target if provided — must be a valid email or empty (to clear)
	if p.Target != "" {
		if _, err := requireEmail(p.Target); err != nil {
			return nil, err
		}
	}

	stalwartID, err := domainIDByName(ctx, p.DomainName)
	if err != nil {
		return nil, err
	}
	if stalwartID == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeNotFound,
			Message: fmt.Sprintf("stalwart has no Domain with name %q — is email_enabled for this domain?", p.DomainName),
		}
	}

	// Patch the domain's catchAllAddress via JMAP x:Domain/set
	if err := updateDomainCatchall(ctx, stalwartID, p.Target); err != nil {
		return nil, err
	}

	return domainCatchallSetResponse{Ok: true, Target: p.Target}, nil
}

// updateDomainCatchall patches a domain's catchAllAddress via JMAP.
// If target is empty, sets catchAllAddress to null (clearing the catch-all).
func updateDomainCatchall(ctx context.Context, domainID string, target string) error {
	// Prepare the patch: if target is empty, use null; otherwise use the email
	var targetValue any
	if target == "" {
		targetValue = nil
	} else {
		targetValue = target
	}

	args := map[string]any{
		"update": map[string]any{
			domainID: map[string]any{
				"catchAllAddress": targetValue,
			},
		},
	}

	var result jmapSetResult
	if err := jmapCall(ctx, "x:Domain/set", args, &result); err != nil {
		return err
	}

	// Check if the update succeeded
	if _, ok := result.Updated[domainID]; ok {
		return nil
	}

	// If not updated, check for errors
	if reason, notOk := result.NotUpdated[domainID]; notOk {
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("stalwart x:Domain/set update refused: %s", string(reason)),
		}
	}

	return &agentwire.AgentError{
		Code:    agentwire.CodeInternal,
		Message: "stalwart x:Domain/set: domain not found or update failed",
	}
}

func init() {
	Default.Register("domain.catchall_set", domainCatchallSetHandler)
}
