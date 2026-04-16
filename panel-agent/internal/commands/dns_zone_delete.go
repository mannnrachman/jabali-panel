package commands

import (
	"context"
	"encoding/json"
	"os/exec"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/pdns"
)

type dnsZoneDeleteParams struct {
	Zone string `json:"zone"`
}

type dnsZoneDeleteResponse struct {
	Zone    string `json:"zone"`
	Deleted bool   `json:"deleted"`
}

func dnsZoneDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dnsZoneDeleteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	if p.Zone == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "zone required"}
	}
	cl := pdns.Default()
	if cl == nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "powerdns backend not available"}
	}
	if err := cl.DeleteZone(p.Zone); err != nil {
		return nil, err
	}
	// NOTIFY so any slaves drop their cached copy.
	_ = exec.CommandContext(ctx, "pdns_control", "notify", p.Zone).Run()
	return dnsZoneDeleteResponse{Zone: p.Zone, Deleted: true}, nil
}

func init() {
	Default.Register("dns.zone.delete", dnsZoneDeleteHandler)
}
