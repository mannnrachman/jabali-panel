package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/pdns"
)

// dnsDNSSECDisableParams is the request shape for dns.dnssec_disable.
type dnsDNSSECDisableParams struct {
	DomainName string `json:"domain_name"`
}

func dnsDNSSECDisableHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p dnsDNSSECDisableParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.DomainName == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "domain_name required"}
	}
	if err := pdns.DisableDNSSEC(ctx, p.DomainName); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	return okBody{Ok: true}, nil
}

func init() {
	Default.Register("dns.dnssec_disable", dnsDNSSECDisableHandler)
}
