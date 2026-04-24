package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/pdns"
)

type dnsDNSSECKeysListParams struct {
	DomainName string `json:"domain_name"`
}

type dnsDNSSECKeysListResponse struct {
	Keys []dnsDNSSECKeyOut `json:"keys"`
}

func dnsDNSSECKeysListHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p dnsDNSSECKeysListParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.DomainName == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "domain_name required"}
	}
	keys, err := pdns.ListKeys(ctx, p.DomainName)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	out := make([]dnsDNSSECKeyOut, 0, len(keys))
	for _, k := range keys {
		out = append(out, dnsDNSSECKeyOut{
			KeyTag:    k.KeyTag,
			KeyType:   k.KeyType,
			Algorithm: k.Algorithm,
			PublicKey: k.PublicKey,
			Active:    k.Active,
		})
	}
	return dnsDNSSECKeysListResponse{Keys: out}, nil
}

func init() {
	Default.Register("dns.dnssec_keys_list", dnsDNSSECKeysListHandler)
}
